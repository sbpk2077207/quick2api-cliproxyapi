package app

import (
	"quick2api-plugin/internal/core"
	"quick2api-plugin/internal/vendors/quick2api"
)

func BuiltinFactories() []core.VendorFactory {
	return []core.VendorFactory{
		{Instances: quick2api.Instances},
	}
}
