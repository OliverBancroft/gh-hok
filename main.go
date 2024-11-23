package main

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	sizeLimit = 1024 * 1024 * 1024 * 1 // 1GB
	host      = "127.0.0.1"
	port      = 8080
)

var (
	exps = []*regexp.Regexp{
		regexp.MustCompile(`^(?:https?://)?github\.com/([^/]+)/([^/]+)/(?:releases|archive)/.*$`),
		regexp.MustCompile(`^(?:https?://)?github\.com/([^/]+)/([^/]+)/(?:blob|raw)/.*$`),
		regexp.MustCompile(`^(?:https?://)?github\.com/([^/]+)/([^/]+)/(?:info|git-).*$`),
		regexp.MustCompile(`^(?:https?://)?raw\.github(?:usercontent|)\.com/([^/]+)/([^/]+)/.+?/.+$`),
		regexp.MustCompile(`^(?:https?://)?gist\.github\.com/([^/]+)/.+?/.+$`),
		regexp.MustCompile(`^(?:https?://)?api\.github\.com/(.*)$`),
	}

	httpClient = &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			DisableKeepAlives:   false,
			DisableCompression:  false,
			MaxIdleConnsPerHost: 10,
		},
	}

	blacklist = make(map[string]struct{})
)

func main() {
	// 加载黑名单
	loadBlacklist("blacklist.txt")

	// 设置为发布模式
	gin.SetMode(gin.ReleaseMode)

	// 创建路由
	router := gin.Default()

	// 添加基本的安全头
	router.Use(func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("X-XSS-Protection", "1; mode=block")
		c.Next()
	})

	// 主页路由
	router.GET("/", func(c *gin.Context) {
		c.String(http.StatusOK, "GitHub Proxy Service Running")
	})

	// 健康检查路由
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	// 所有其他路由走代理处理
	router.NoRoute(handler)

	// 启动服务器
	fmt.Printf("Server starting on %s:%d\n", host, port)
	if err := router.Run(fmt.Sprintf("%s:%d", host, port)); err != nil {
		fmt.Printf("Error starting server: %v\n", err)
	}
}

func loadBlacklist(filename string) {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Printf("Warning: Could not load blacklist file: %v\n", err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		user := strings.TrimSpace(scanner.Text())
		if user != "" && !strings.HasPrefix(user, "#") {
			blacklist[user] = struct{}{}
		}
	}
}

func handler(c *gin.Context) {
	rawPath := strings.TrimPrefix(c.Request.URL.RequestURI(), "/")
	if rawPath == "" {
		c.String(http.StatusBadRequest, "Empty path")
		return
	}

	re := regexp.MustCompile(`^(http:|https:)?/?/?(.*)`)
	matches := re.FindStringSubmatch(rawPath)
	if matches == nil {
		c.String(http.StatusBadRequest, "Invalid URL format")
		return
	}

	rawPath = "https://" + matches[2]

	// 检查是否是 API 请求
	if strings.Contains(rawPath, "api.github.com") {
		handleAPIRequest(c, rawPath)
		return
	}

	matched := false
	var user string
	for _, exp := range exps {
		if match := exp.FindStringSubmatch(rawPath); match != nil {
			matched = true
			if len(match) > 1 {
				user = match[1]
			}
			break
		}
	}

	if !matched {
		c.String(http.StatusForbidden, "Invalid GitHub URL format")
		return
	}

	if user != "" {
		if _, blocked := blacklist[user]; blocked {
			c.String(http.StatusForbidden, "Access denied: User is blacklisted")
			return
		}
	}

	if exps[1].MatchString(rawPath) {
		rawPath = strings.Replace(rawPath, "/blob/", "/raw/", 1)
	}

	proxy(c, rawPath)
}

func handleAPIRequest(c *gin.Context, rawPath string) {
	req, err := http.NewRequest(c.Request.Method, rawPath, c.Request.Body)
	if err != nil {
		c.String(http.StatusInternalServerError, fmt.Sprintf("Error creating API request: %v", err))
		return
	}

	// 复制请求头
	for key, values := range c.Request.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		c.String(http.StatusBadGateway, fmt.Sprintf("Error proxying API request: %v", err))
		return
	}
	defer resp.Body.Close()

	// 复制响应头
	for key, values := range resp.Header {
		for _, value := range values {
			c.Header(key, value)
		}
	}

	c.Status(resp.StatusCode)

	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		fmt.Printf("Error copying API response: %v\n", err)
	}
}

func proxy(c *gin.Context, u string) {
	req, err := http.NewRequest(c.Request.Method, u, c.Request.Body)
	if err != nil {
		c.String(http.StatusInternalServerError, fmt.Sprintf("Error creating request: %v", err))
		return
	}

	// 复制请求头
	for key, values := range c.Request.Header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "GithubProxy/1.0")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		c.String(http.StatusBadGateway, fmt.Sprintf("Error proxying request: %v", err))
		return
	}
	defer resp.Body.Close()

	// 处理大文件
	if contentLength := resp.Header.Get("Content-Length"); contentLength != "" {
		if size, err := strconv.ParseInt(contentLength, 10, 64); err == nil && size > sizeLimit {
			c.Redirect(http.StatusTemporaryRedirect, resp.Request.URL.String())
			return
		}
	}

	// 设置响应头
	c.Header("Cache-Control", "public, max-age=604800")
	for key, values := range resp.Header {
		for _, value := range values {
			c.Header(key, value)
		}
	}

	c.Status(resp.StatusCode)
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		fmt.Printf("Error copying response: %v\n", err)
	}
}
