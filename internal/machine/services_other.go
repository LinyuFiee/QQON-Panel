//go:build !linux

package machine

import "context"

func newServicesSource() (func(context.Context) servicesView, servicesView) {
	return nil, unknownServices("当前平台不支持 systemd 服务状态")
}
