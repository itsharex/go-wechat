package module

import (
	"github.com/nilorg/go-wechat/v2/proxy/module/config"
	"github.com/nilorg/go-wechat/v2/proxy/module/store"
)

// Init 初始化 module
func Init() {
	config.Init()
	store.Init()
}
