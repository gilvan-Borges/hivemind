//go:build !windows && !linux

package resident

import (
	"context"
	"fmt"
)

func (osSurface) OpenURL(context.Context, string) error {
	return fmt.Errorf("browser experimental sem isolamento não está disponível neste sistema")
}

func (osSurface) Click(context.Context, int, int) error {
	return fmt.Errorf("desktop experimental sem isolamento não está disponível neste sistema")
}

func (osSurface) TypeText(context.Context, string) error {
	return fmt.Errorf("desktop experimental sem isolamento não está disponível neste sistema")
}
