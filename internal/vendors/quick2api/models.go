package quick2api

// 模型清单：Quick 的 agent_mode 档位对外暴露为四个模型。
// smart → Claude Opus 5.5；balanced → Sonnet；fast → Haiku；advanced → smart。

import (
	"quick2api-plugin/cpasdk/pluginapi"
	"quick2api-plugin/internal/core"
)

var modelDefs = []struct {
	ID   string
	Mode string
	Desc string
}{
	{"quick-smart", "smart", "Amazon Quick 智能档（Opus 级推理，1M 上下文）"},
	{"quick-balanced", "balanced", "Amazon Quick 均衡档（Sonnet 级）"},
	{"quick-fast", "fast", "Amazon Quick 快速档（Haiku 级）"},
	{"quick-advanced", "advanced", "Amazon Quick 进阶档（服务端路由）"},
}

func staticModels() []pluginapi.ModelInfo {
	return core.ConvertModels(modelDefs, func(m struct {
		ID   string
		Mode string
		Desc string
	}) (core.ModelDescriptor, bool) {
		return core.ModelDescriptor{
			ID:                 m.ID,
			Name:               m.ID,
			Description:        m.Desc,
			ContextLength:      1000000,
			MaxOutputTokens:    65536,
			SupportsTools:      true,
			SupportsImages:     true,
			CanDisableThinking: true,
		}, true
	})
}

func modeForModel(id string) string {
	for _, m := range modelDefs {
		if m.ID == id {
			return m.Mode
		}
	}
	switch id {
	case "smart", "balanced", "fast", "advanced":
		return id
	}
	return "smart"
}
