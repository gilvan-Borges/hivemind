package mcpx

import (
	"context"
	"fmt"
	"sync"
)

const TransportInProc = "inproc"

// InProcHandler é um servidor MCP que mora neste processo. O browser do
// Agent Hive usa isto: não há binário externo para procurar no PATH.
type InProcHandler struct {
	Tools func(ctx context.Context) ([]RemoteTool, error)
	Call  func(ctx context.Context, name string, args map[string]any) (string, error)
	Close func() error
}

type inProcSession struct{ h InProcHandler }

func (s inProcSession) Tools(ctx context.Context) ([]RemoteTool, error) {
	if s.h.Tools == nil {
		return nil, nil
	}
	return s.h.Tools(ctx)
}

func (s inProcSession) Call(ctx context.Context, name string, args map[string]any) (string, error) {
	if s.h.Call == nil {
		return "", fmt.Errorf("servidor interno sem chamada")
	}
	return s.h.Call(ctx, name, args)
}

func (s inProcSession) Close() error {
	if s.h.Close == nil {
		return nil
	}
	return s.h.Close()
}

var (
	inProcMu   sync.Mutex
	inProcOpen = map[string]func(context.Context) (InProcHandler, error){}
)

// RegisterInProc entrega o abridor de um servidor interno. O pacote que
// implementa o servidor chama isto no init.
func RegisterInProc(name string, open func(context.Context) (InProcHandler, error)) {
	inProcMu.Lock()
	defer inProcMu.Unlock()
	if open == nil {
		delete(inProcOpen, name)
		return
	}
	inProcOpen[name] = open
}

func openInProc(ctx context.Context, s Server) (session, error) {
	inProcMu.Lock()
	open := inProcOpen[s.Name]
	inProcMu.Unlock()
	if open == nil {
		return nil, fmt.Errorf("servidor interno %s não está registrado neste processo", s.Name)
	}
	h, err := open(ctx)
	if err != nil {
		return nil, err
	}
	return inProcSession{h: h}, nil
}
