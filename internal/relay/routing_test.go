package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gilvan-Borges/HiveMind/relaywire"
)

func dial(t *testing.T, base, token string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(base, "http") + "/api/v1/ws"
	c, _, err := websocket.Dial(context.Background(), url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(websocket.StatusNormalClosure, "") })
	return c
}

func writeEnv(t *testing.T, c *websocket.Conn, env relaywire.Envelope) {
	t.Helper()
	if env.V == 0 {
		env.V = 1
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func readEnv(t *testing.T, c *websocket.Conn, want string) relaywire.Envelope {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Until(deadline))
		_, data, err := c.Read(ctx)
		cancel()
		if err != nil {
			t.Fatalf("lendo %s: %v", want, err)
		}
		var env relaywire.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatal(err)
		}
		if env.Type == relaywire.TypePing || env.Type == relaywire.TypePong {
			continue
		}
		if want != "" && env.Type != want {
			t.Fatalf("frame %s, esperava %s (%s)", env.Type, want, data)
		}
		return env
	}
	t.Fatalf("timeout esperando %s", want)
	return relaywire.Envelope{}
}

func waitUntil(t *testing.T, d time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("condição não atingida a tempo")
}

func readMaybe(c *websocket.Conn, d time.Duration) (relaywire.Envelope, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		return relaywire.Envelope{}, false
	}
	var env relaywire.Envelope
	if json.Unmarshal(data, &env) != nil {
		return relaywire.Envelope{}, false
	}
	return env, true
}

func seal(t *testing.T, from *relaywire.KeyPair, peer [32]byte, v any) json.RawMessage {
	t.Helper()
	raw, err := from.SealJSON(peer, v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRoutingIsolationResumeAndQueue(t *testing.T) {
	ts, store := newTestServer(t)
	owner := login(t, ts.URL, "owner@example.com", "senha-inicial-ok")
	otherRes := doJSON(t, http.MethodPost, ts.URL+"/api/v1/accounts", owner.AccessToken, map[string]string{
		"email": "outro@example.com", "password": "senha-do-outro",
	}, nil)
	if otherRes.StatusCode != http.StatusCreated {
		t.Fatal(otherRes.Status)
	}
	other := login(t, ts.URL, "outro@example.com", "senha-do-outro")

	ideKey, err := relaywire.NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	appKey, err := relaywire.NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	ideKeyB, err := relaywire.NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	appKeyB, err := relaywire.NewKeyPair()
	if err != nil {
		t.Fatal(err)
	}

	var created struct {
		DeviceToken string `json:"deviceToken"`
		Device      struct {
			ID string `json:"id"`
		} `json:"device"`
	}
	res := doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices", owner.AccessToken, map[string]string{
		"name": "pc-trabalho", "publicKey": ideKey.PublicB64(),
	}, &created)
	if res.StatusCode != http.StatusCreated || !strings.HasPrefix(created.DeviceToken, relaywire.TokenDevicePrefix) {
		t.Fatalf("device: %d %#v", res.StatusCode, created)
	}
	res = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/"+created.Device.ID+"/peer-key", owner.AccessToken, map[string]string{
		"publicKey": appKey.PublicB64(),
	}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("peer: %d", res.StatusCode)
	}

	var createdB struct {
		DeviceToken string `json:"deviceToken"`
		Device      struct {
			ID string `json:"id"`
		} `json:"device"`
	}
	res = doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices", owner.AccessToken, map[string]string{
		"name": "pc-casa", "publicKey": ideKeyB.PublicB64(),
	}, &createdB)
	if res.StatusCode != http.StatusCreated {
		t.Fatal(res.Status)
	}
	if res := doJSON(t, http.MethodPost, ts.URL+"/api/v1/devices/"+createdB.Device.ID+"/peer-key", owner.AccessToken, map[string]string{
		"publicKey": appKeyB.PublicB64(),
	}, nil); res.StatusCode != http.StatusOK {
		t.Fatal(res.Status)
	}

	// Outra conta não abre o socket desta IDE.
	intruder := dial(t, ts.URL, other.AccessToken)
	helloPayload, _ := json.Marshal(relaywire.Hello{PublicKey: appKey.PublicB64()})
	writeEnv(t, intruder, relaywire.Envelope{
		Type: relaywire.TypeHello, Role: relaywire.RoleApp, DeviceID: created.Device.ID, ID: "x", Payload: helloPayload,
	})
	denied := readEnv(t, intruder, relaywire.TypeError)
	var eb relaywire.ErrorBody
	if json.Unmarshal(denied.Payload, &eb) != nil || eb.Code != relaywire.CodeNotFound {
		t.Fatalf("intruso: %#v", denied)
	}

	ide := dial(t, ts.URL, created.DeviceToken)
	ideHello, _ := json.Marshal(relaywire.Hello{PublicKey: ideKey.PublicB64()})
	writeEnv(t, ide, relaywire.Envelope{
		Type: relaywire.TypeHello, Role: relaywire.RoleIDE, DeviceID: created.Device.ID, ID: "ide", Payload: ideHello,
	})
	if got := readEnv(t, ide, relaywire.TypeHelloOK); got.DeviceID != created.Device.ID {
		t.Fatalf("hello ide: %#v", got)
	}

	ideB := dial(t, ts.URL, createdB.DeviceToken)
	ideBHello, _ := json.Marshal(relaywire.Hello{PublicKey: ideKeyB.PublicB64()})
	writeEnv(t, ideB, relaywire.Envelope{
		Type: relaywire.TypeHello, Role: relaywire.RoleIDE, DeviceID: createdB.Device.ID, ID: "ideb", Payload: ideBHello,
	})
	_ = readEnv(t, ideB, relaywire.TypeHelloOK)

	app := dial(t, ts.URL, owner.AccessToken)
	appHello, _ := json.Marshal(relaywire.Hello{PublicKey: appKey.PublicB64()})
	writeEnv(t, app, relaywire.Envelope{
		Type: relaywire.TypeHello, Role: relaywire.RoleApp, DeviceID: created.Device.ID, ID: "app", Payload: appHello,
	})
	okFrame := readEnv(t, app, relaywire.TypeHelloOK)
	var okBody relaywire.HelloOK
	if err := json.Unmarshal(okFrame.Payload, &okBody); err != nil || !okBody.E2EReady || !okBody.Online {
		t.Fatalf("hello app: %s %v", okFrame.Payload, err)
	}

	// RPC em claro é recusado. A outra IDE não vê nada.
	writeEnv(t, app, relaywire.Envelope{
		Type: relaywire.TypeRPC, ID: "plain", DeviceID: created.Device.ID,
		Payload: json.RawMessage(`{"method":"GET","path":"/api/v1/projects"}`),
	})
	plainErr := readEnv(t, app, relaywire.TypeError)
	if json.Unmarshal(plainErr.Payload, &eb) != nil || eb.Code != relaywire.CodeEncryption {
		t.Fatalf("plaintext: %s", plainErr.Payload)
	}
	if _, got := readMaybe(ideB, 150*time.Millisecond); got {
		t.Fatal("a outra IDE recebeu o rpc recusado")
	}

	// deviceId de outra IDE na mesma conta é recusado e não é encaminhado.
	writeEnv(t, app, relaywire.Envelope{
		Type: relaywire.TypeRPC, ID: "cross", DeviceID: createdB.Device.ID, Enc: relaywire.EncNaClBox,
		Payload: seal(t, appKey, ideKeyB.Public, relaywire.RPCRequest{Method: "GET", Path: "/api/v1/projects"}),
	})
	cross := readEnv(t, app, relaywire.TypeError)
	if json.Unmarshal(cross.Payload, &eb) != nil || eb.Code != relaywire.CodeForbidden {
		t.Fatalf("cross: %s", cross.Payload)
	}
	if _, got := readMaybe(ideB, 150*time.Millisecond); got {
		t.Fatal("IDE B recebeu rpc da conexão da IDE A")
	}

	const marker = "SEGREDO-APROVACAO-9f3c"
	req := relaywire.RPCRequest{Method: "POST", Path: "/api/v1/approvals/resolve", Body: json.RawMessage(`{"approvalId":"a1","approved":true,"marker":"` + marker + `"}`)}
	writeEnv(t, app, relaywire.Envelope{
		Type: relaywire.TypeRPC, ID: "rpc1", DeviceID: created.Device.ID, Enc: relaywire.EncNaClBox,
		Payload: seal(t, appKey, ideKey.Public, req),
	})
	gotRPC := readEnv(t, ide, relaywire.TypeRPC)
	if bytes.Contains(gotRPC.Payload, []byte(marker)) {
		t.Fatal("o relay entregou o plaintext do rpc")
	}
	var opened relaywire.RPCRequest
	if err := ideKey.OpenJSON(appKey.Public, gotRPC.Payload, &opened); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(opened.Body, []byte(marker)) || opened.Path != "/api/v1/approvals/resolve" {
		t.Fatalf("rpc aberto: %#v", opened)
	}
	if _, sneak := readMaybe(ideB, 150*time.Millisecond); sneak {
		t.Fatal("IDE B viu o rpc da IDE A")
	}
	writeEnv(t, ide, relaywire.Envelope{
		Type: relaywire.TypeRPCResult, ID: gotRPC.ID, DeviceID: created.Device.ID, Enc: relaywire.EncNaClBox,
		Payload: seal(t, ideKey, appKey.Public, relaywire.RPCResult{Status: 200, Body: json.RawMessage(`{"resolved":true}`)}),
	})
	result := readEnv(t, app, relaywire.TypeRPCResult)
	var rr relaywire.RPCResult
	if err := appKey.OpenJSON(ideKey.Public, result.Payload, &rr); err != nil || rr.Status != 200 {
		t.Fatalf("resultado: %v %#v", err, rr)
	}

	evInner := map[string]any{"type": "lane_event", "data": map[string]any{"marker": marker}}
	writeEnv(t, ide, relaywire.Envelope{
		Type: relaywire.TypeEvent, ID: "e1", DeviceID: created.Device.ID, Enc: relaywire.EncNaClBox,
		Payload: seal(t, ideKey, appKey.Public, evInner),
	})
	ev := readEnv(t, app, relaywire.TypeEvent)
	if ev.Seq != 1 {
		t.Fatalf("seq: %d", ev.Seq)
	}
	if bytes.Contains(ev.Payload, []byte(marker)) {
		t.Fatal("evento em claro no fio")
	}
	stored, err := store.EventsAfter(created.Device.ID, 0, 10)
	if err != nil || len(stored) != 1 {
		t.Fatalf("log: %v %d", err, len(stored))
	}
	if bytes.Contains(stored[0].Body, []byte(marker)) {
		t.Fatal("o sqlite do relay guarda o plaintext")
	}

	_ = app.Close(websocket.StatusGoingAway, "")
	writeEnv(t, ide, relaywire.Envelope{
		Type: relaywire.TypeEvent, ID: "e2", DeviceID: created.Device.ID, Enc: relaywire.EncNaClBox,
		Payload: seal(t, ideKey, appKey.Public, map[string]any{"type": "approval_resolved", "data": map[string]any{"approvalId": "a1"}}),
	})
	waitUntil(t, 2*time.Second, func() bool {
		evs, err := store.EventsAfter(created.Device.ID, 0, 10)
		return err == nil && len(evs) >= 2
	})

	app2 := dial(t, ts.URL, owner.AccessToken)
	resume, _ := json.Marshal(relaywire.Hello{ResumeAfter: ev.Seq, PublicKey: appKey.PublicB64()})
	writeEnv(t, app2, relaywire.Envelope{
		Type: relaywire.TypeHello, Role: relaywire.RoleApp, DeviceID: created.Device.ID, ID: "app2", Payload: resume,
	})
	_ = readEnv(t, app2, relaywire.TypeHelloOK)
	resumed := readEnv(t, app2, relaywire.TypeEvent)
	if resumed.Seq != 2 {
		t.Fatalf("resume seq %d", resumed.Seq)
	}
	var resumedInner map[string]any
	if err := appKey.OpenJSON(ideKey.Public, resumed.Payload, &resumedInner); err != nil {
		t.Fatal(err)
	}
	if resumedInner["type"] != "approval_resolved" {
		t.Fatalf("evento retomado: %#v", resumedInner)
	}

	// Aprovação enviada com a IDE fora do ar é entregue quando ela volta.
	_ = ide.Close(websocket.StatusGoingAway, "")
	waitUntil(t, 2*time.Second, func() bool {
		var view map[string]any
		res := doJSON(t, http.MethodGet, ts.URL+"/api/v1/devices/"+created.Device.ID, owner.AccessToken, nil, &view)
		online, _ := view["online"].(bool)
		return res.StatusCode == http.StatusOK && !online
	})
	writeEnv(t, app2, relaywire.Envelope{
		Type: relaywire.TypeRPC, ID: "queued-approval", DeviceID: created.Device.ID, Enc: relaywire.EncNaClBox,
		Payload: seal(t, appKey, ideKey.Public, relaywire.RPCRequest{
			Method: "POST", Path: "/api/v1/approvals/resolve",
			Body: json.RawMessage(`{"approvalId":"a2","approved":true}`),
		}),
	})
	ide2 := dial(t, ts.URL, created.DeviceToken)
	writeEnv(t, ide2, relaywire.Envelope{
		Type: relaywire.TypeHello, Role: relaywire.RoleIDE, DeviceID: created.Device.ID, ID: "ide2", Payload: ideHello,
	})
	_ = readEnv(t, ide2, relaywire.TypeHelloOK)
	queued := readEnv(t, ide2, relaywire.TypeRPC)
	if queued.ID != "queued-approval" {
		t.Fatalf("fila: %#v", queued)
	}
	var qreq relaywire.RPCRequest
	if err := ideKey.OpenJSON(appKey.Public, queued.Payload, &qreq); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(qreq.Body, []byte("a2")) {
		t.Fatalf("fila aberta: %#v", qreq)
	}
	writeEnv(t, ide2, relaywire.Envelope{
		Type: relaywire.TypeRPCResult, ID: queued.ID, DeviceID: created.Device.ID, Enc: relaywire.EncNaClBox,
		Payload: seal(t, ideKey, appKey.Public, relaywire.RPCResult{Status: 200, Body: json.RawMessage(`{"resolved":true}`)}),
	})
	back := readEnv(t, app2, relaywire.TypeRPCResult)
	if back.ID != "queued-approval" {
		t.Fatalf("resultado da fila: %#v", back)
	}
}
