package server

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/nilorg/go-wechat/v2/gateway/middleware"
	"github.com/nilorg/go-wechat/v2/proxy/module/config"
	"github.com/nilorg/go-wechat/v2/proxy/module/logger"
	"github.com/nilorg/go-wechat/v2/proxy/module/store"
)

var Transport http.RoundTripper = &http.Transport{
	Proxy: http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}).DialContext,
	ForceAttemptHTTP2:     true,
	MaxIdleConns:          100,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   10 * time.Second,
	ExpectContinueTimeout: 1 * time.Second,
}

var srv *http.Server

func HTTP() {
	engine := gin.Default()
	engine.Use(middleware.Header())
	engine.GET("/:appid/*path", checkAppID, proxy)
	engine.POST("/:appid/*path", checkAppID, proxy)
	srv = &http.Server{
		Addr:    ":8080",
		Handler: engine,
	}
	// Initializing the server in a goroutine so that
	// it won't block the graceful shutdown handling below
	go func() {
		if err := srv.ListenAndServe(); err != nil && errors.Is(err, http.ErrServerClosed) {
			logger.Sugared.Errorf("listen: %s", err)
		}
	}()
}

func Shutdown(ctx context.Context) {
	if err := srv.Shutdown(ctx); err != nil {
		logger.Sugared.Fatal("Server forced to shutdown:", err)
	}
	logger.Sugared.Info("Http Server exiting")
}

func proxy(ctx *gin.Context) {
	appID := ctx.Param("appid")
	path := ctx.Param("path")
	logger.Sugared.Debugf("APPID:%s,PATH:%s", appID, path)
	appConfig := config.GetApp(appID)
	// 组织要访问的微信接口
	proxyBaseURL := "https://api.weixin.qq.com"
	proxyQuery := ctx.Request.URL.Query()
	switch path {
	case "/sns/oauth2/access_token", "/sns/jscode2session":
		proxyQuery.Set("appid", appConfig.ID)
		proxyQuery.Set("secret", appConfig.Secret)
	case "/sns/oauth2/refresh_token":
		proxyQuery.Set("appid", appConfig.ID)
	case "/cgi-bin/showqrcode":
		proxyBaseURL = "https://mp.weixin.qq.com"
		proxyQuery.Set("ticket", getRedisValue(appConfig.RedisJsAPITicketKey))
	case "/cgi-bin/gettoken":
		proxyBaseURL = "https://qyapi.weixin.qq.com"
		proxyQuery.Set("corpid", appConfig.ID)
		proxyQuery.Set("corpsecret", appConfig.Secret)
	default:
		proxyQuery.Set("access_token", getRedisValue(appConfig.RedisAccessTokenKey))
	}

	var (
		proxyURL *url.URL
		err      error
	)
	proxyURL, err = url.Parse(proxyBaseURL + path)
	if err != nil {
		ctx.Status(http.StatusBadRequest)
		return
	}
	logger.Sugared.Debugf("请求微信地址：%s?%s", proxyURL, proxyQuery.Encode())
	proxyURL.RawQuery = proxyQuery.Encode()
	proxyReq := *ctx.Request // 复制请求信息
	proxyReq.URL = proxyURL  // 设置代理URL

	var proxyResp *http.Response
	proxyResp, err = Transport.RoundTrip(&proxyReq)
	if err != nil {
		logger.Sugared.Errorf("访问源%s错误%v", proxyReq.URL.Host, err)
		ctx.String(http.StatusBadGateway, "请求接口出错")
		return
	}
	defer proxyResp.Body.Close()
	for key, value := range proxyResp.Header { // 设置响应Header
		logger.Sugared.Debugf("Header: %s:%v", key, value)
		if strings.EqualFold(key, "Content-Length") || strings.EqualFold(key, "Connection") {
			continue
		}
		for _, v := range value {
			ctx.Writer.Header().Add(key, v)
		}
	}
	ctx.Writer.WriteHeader(proxyResp.StatusCode)
	io.Copy(ctx.Writer, proxyResp.Body)
}

// checkAppID 检查AppID
func checkAppID(ctx *gin.Context) {
	appID := ctx.Param("appid")
	logger.Sugared.Debugf("检查AppID:%s是否存在", appID)
	if !config.ExistAppID(appID) {
		logger.Sugared.Debugf("未检查到AppID:%s", appID)
		ctx.Status(404)
		ctx.Abort()
		return
	}
	ctx.Next()
}

func getRedisValue(key string) string {
	bytes, err := store.RedisClient.Get(context.Background(), key).Bytes()
	if err == redis.Nil {
		return ""
	} else if err != nil {
		log.Println(err)
		return ""
	}
	return string(bytes)
}
