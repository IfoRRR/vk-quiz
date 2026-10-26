package quiz

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestOriginIgnoresGatewayPort(t *testing.T) {
	request := httptest.NewRequest("GET", "/ws", nil)
	request.Host = "132.243.194.181"
	request.Header.Set("Origin", "http://132.243.194.181:8081")
	if !quizOrigin(request) {
		t.Fatal("same host with the gateway port should pass")
	}
	request.Header.Set("Origin", "http://evil.example:8081")
	if quizOrigin(request) {
		t.Fatal("another host should fail")
	}
}

func TestSocketRound(t *testing.T) {
	handler := NewHandler(New(), log.New(io.Discard, "", 0), t.TempDir())
	mux := http.NewServeMux()
	handler.Mount(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	host := dial(t, server.URL)
	defer host.Close()
	correct := 1
	send(t, host, map[string]any{
		"op":    "create",
		"title": "Событие",
		"questions": []map[string]any{{
			"text":    "Как войти?",
			"options": []string{"По паролю", "По коду"},
			"correct": correct,
		}},
	})
	hello := read(t, host)
	if hello["type"] != "hello" || hello["role"] != "host" {
		t.Fatalf("hello %+v", hello)
	}
	code, _ := hello["code"].(string)
	if read(t, host)["type"] != "state" {
		t.Fatal("host state missing")
	}

	player := dial(t, server.URL)
	defer player.Close()
	send(t, player, map[string]any{"op": "join", "code": code, "name": "Катя"})
	if read(t, player)["role"] != "player" {
		t.Fatal("player hello")
	}
	if read(t, player)["phase"] != "lobby" {
		t.Fatal("player lobby")
	}
	if read(t, host)["totalPlayers"].(float64) != 1 {
		t.Fatal("host did not see the player")
	}

	send(t, host, map[string]any{"op": "start"})
	hostQ := read(t, host)
	playerQ := read(t, player)
	question := playerQ["question"].(map[string]any)
	if _, ok := question["correct"]; ok {
		t.Fatal("player received the key")
	}
	if hostQ["question"].(map[string]any)["counts"] == nil {
		t.Fatal("host should see live counts")
	}

	send(t, player, map[string]any{"op": "answer", "option": 1})
	_ = read(t, host)
	_ = read(t, player)
	send(t, host, map[string]any{"op": "reveal"})
	_ = read(t, host)
	shown := read(t, player)
	you := shown["you"].(map[string]any)
	if you["score"].(float64) < 100 {
		t.Fatalf("score %+v", you)
	}
	send(t, host, map[string]any{"op": "next"})
	done := read(t, host)
	if done["phase"] != "done" {
		t.Fatalf("phase %v", done["phase"])
	}
}

func TestPreviewOpensWithoutAccount(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "quiz.json"))
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(New(), log.New(io.Discard, "", 0), t.TempDir())
	handler.store = store
	mux := http.NewServeMux()
	handler.Mount(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	host := dial(t, server.URL)
	defer host.Close()
	send(t, host, map[string]any{
		"op":      "create",
		"preview": true,
		"title":   "Просмотр",
		"seconds": 30,
		"questions": []map[string]any{{
			"text":    "Как войти в просмотр?",
			"options": []string{"Почта", "Сразу"},
			"correct": 1,
		}},
	})
	hello := read(t, host)
	if hello["role"] != "host" {
		t.Fatalf("hello %+v", hello)
	}
	_ = read(t, host)
	code, _ := hello["code"].(string)

	player := dial(t, server.URL)
	defer player.Close()
	send(t, player, map[string]any{"op": "join", "code": code, "name": "Вы"})
	if read(t, player)["role"] != "player" {
		t.Fatal("name join failed")
	}
	_ = read(t, player)
	_ = read(t, host)

	send(t, host, map[string]any{"op": "start"})
	_ = read(t, host)
	_ = read(t, player)
	send(t, player, map[string]any{"op": "answer", "option": 1})
	_ = read(t, host)
	_ = read(t, player)
	send(t, host, map[string]any{"op": "reveal"})
	_ = read(t, host)
	shown := read(t, player)
	you := shown["you"].(map[string]any)
	if you["score"].(float64) < 100 {
		t.Fatalf("score %+v", you)
	}
}

func dial(t *testing.T, httpURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + httpURL[len("http"):] + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	return conn
}

func send(t *testing.T, conn *websocket.Conn, payload any) {
	t.Helper()
	if err := conn.WriteJSON(payload); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatal(err)
	}
	return msg
}
