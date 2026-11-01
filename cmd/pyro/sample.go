package main

import (
	"github.com/pyrohq/pyro/internal/store"
	"github.com/pyrohq/pyro/pkg/engine/model"
)

// seedSample adds a small example collection covering each built-in protocol.
func seedSample(ws *store.Workspace) error {
	if err := ws.CreateCollection("httpbin", model.Collection{
		Name:        "httpbin examples",
		Description: "Example requests against httpbin.org and public echo servers.",
		Variables:   []model.Variable{{Key: "baseUrl", Value: "https://httpbin.org"}},
	}); err != nil {
		return err
	}
	reqs := map[string]*model.Request{
		"httpbin/get-json": {
			Kind: model.KindHTTP, Name: "Get JSON", Method: "GET", URL: "{{baseUrl}}/json",
			Assertions: []model.Assertion{{Type: "status", Op: "eq", Value: "200"}},
		},
		"httpbin/post-echo": {
			Kind: model.KindHTTP, Name: "Post echo", Method: "POST", URL: "{{baseUrl}}/post",
			Headers: []model.KeyValue{{Key: "Content-Type", Value: "application/json"}},
			Body:    &model.Body{Type: model.BodyJSON, Content: "{\n  \"hello\": \"pyro\"\n}"},
			Extract: []model.Extractor{{Var: "echoed", From: "jsonpath", Path: "$.json.hello"}},
		},
		"httpbin/auth/bearer": {
			Kind: model.KindHTTP, Name: "Bearer auth", Method: "GET", URL: "{{baseUrl}}/bearer",
			Auth: &model.Auth{Type: "bearer", Params: map[string]string{"token": "{{token}}"}},
		},
		"httpbin/realtime/echo-ws": {
			Kind: model.KindWebSocket, Name: "WebSocket echo", URL: "wss://echo.websocket.org",
			Messages: []model.Message{{Name: "Hello", Content: "hello from pyro"}},
		},
		"httpbin/realtime/chat-sio": {
			Kind: model.KindSocketIO, Name: "Socket.IO chat", URL: "http://localhost:3000",
			SocketIO: &model.SocketIOConfig{Namespace: "/", Listen: []string{"chat message"}},
			Messages: []model.Message{{Event: "chat message", Content: `["hi"]`}},
		},
	}
	for p, r := range reqs {
		if err := ws.WriteRequest(p, r); err != nil {
			return err
		}
	}
	return ws.WriteEnvironment("local", &model.Environment{
		Name:      "local",
		Variables: []model.Variable{{Key: "token", Value: "dev-token", Secret: true}},
	})
}
