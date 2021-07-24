package server

import (
	"context"
	"errors"
	"io/ioutil"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	crypter "github.com/heroicyang/wechat-crypter"
	"github.com/nilorg/go-wechat/v2/gateway/middleware"
	"github.com/nilorg/go-wechat/v2/gateway/models"
	"github.com/nilorg/go-wechat/v2/gateway/module/config"
	"github.com/nilorg/go-wechat/v2/gateway/module/logger"
	"github.com/nilorg/sdk/convert"
)

var srv *http.Server

func HTTP() {
	engine := gin.Default()
	engine.Use(middleware.Header())
	engine.GET("/:appid", checkAppID, AcceptGET)
	engine.POST("/:appid", checkAppID, AcceptPOST)
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

func AcceptGET(ctx *gin.Context) {
	var model models.WechatRequestModel
	if err := ctx.ShouldBindWith(&model, binding.Query); err != nil {
		logger.Sugared.Errorf("ctx.ShouldBindWith: %v", err)
		ctx.Status(400)
		return
	}
	appID := ctx.Param("appid")
	appConfig := config.GetApp(appID)
	msgCrypter, err := crypter.NewMessageCrypter(appConfig.Token, appConfig.EncodingAESKey, appID)
	if err != nil {
		logger.Sugared.Errorf("crypter.NewMessageCrypter: %v", err)
		ctx.Status(400)
		return
	}
	if model.Signature != msgCrypter.GetSignature(convert.ToString(model.Timestamp), convert.ToString(model.Nonce), "") {
		ctx.Status(400)
		return
	}
	ctx.String(http.StatusOK, model.Echostr)
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

// 接收
func AcceptPOST(ctx *gin.Context) {
	appID := ctx.Param("appid")
	appConfig := config.GetApp(appID)
	if appConfig.EncryptionMethod == "secure" {
		bytes, err := ioutil.ReadAll(ctx.Request.Body)
		if err != nil {
			logger.Sugared.Errorf("ioutil.ReadAll: %v", err)
			ctx.Status(400)
			return
		}
		msgCrypter, err := crypter.NewMessageCrypter(appConfig.Token, appConfig.EncodingAESKey, appID)
		if err != nil {
			logger.Sugared.Errorf("crypter.NewMessageCrypter: %v", err)
			ctx.Status(400)
			return
		}
		// 解析报文数据
		var msg *models.AcceptEncryptMessage
		if appConfig.DataType == "xml" {
			msg, err = models.AcceptEncryptMessageParseForXML(bytes)
			if err != nil {
				logger.Sugared.Errorf("models.AcceptEncryptMessageParseForXML: %v", err)
				ctx.Status(400)
				return
			}
		} else if appConfig.DataType == "json" {
			msg, err = models.AcceptEncryptMessageParseForJSON(bytes)
			if err != nil {
				logger.Sugared.Errorf("models.AcceptEncryptMessageParseForJSON: %v", err)
				ctx.Status(400)
				return
			}
		} else {
			logger.Sugared.Error("数据格式不正确")
			ctx.Status(400)
			return
		}
		// 解析加密报文数据
		var encryptBytes []byte
		var inAppID string
		encryptBytes, inAppID, err = msgCrypter.Decrypt(string(msg.Encrypt))
		if err != nil {
			logger.Sugared.Warn("加密报文解析失败")
			ctx.Status(400)
			return
		}
		if appID != inAppID {
			logger.Sugared.Warn("加密报文AppID不匹配")
			ctx.Status(400)
			return
		}
		// 输出解密后的报文
		logger.Sugared.Info(string(encryptBytes))
	} else if appConfig.EncryptionMethod == "compatible" {

	} else {
		bytes, err := ioutil.ReadAll(ctx.Request.Body)
		if err != nil {
			logger.Sugared.Errorf("ioutil.ReadAll: %v", err)
			ctx.Status(400)
			return
		}
		logger.Sugared.Info(string(bytes))
	}
}
