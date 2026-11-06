//go:build linux

package machine

import (
	"context"
	"os"
)

func newServicesSource() (func(context.Context) servicesView, servicesView) {
	if info, err := os.Stat("/run/systemd/system"); err != nil || !info.IsDir() {
		return nil, unknownServices("当前主机没有运行 systemd")
	}
	// Fixed absolute paths only: never resolve an attacker-controlled PATH.
	for _, executable := range []string{"/usr/bin/systemctl", "/bin/systemctl"} {
		info, err := os.Stat(executable)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 {
			return func(ctx context.Context) servicesView {
				return queryServices(ctx, executable, runBoundedCommand)
			}, unknownServices("正在采集 systemd 服务状态")
		}
	}
	return nil, unknownServices("未找到 systemctl")
}
