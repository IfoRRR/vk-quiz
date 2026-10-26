package quiz

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin:     quizOrigin,
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// quizOrigin — проверка чата плюс случай, когда шлюз отдаёт Host без порта,
// а страница открыта как http://ip:8081.
func quizOrigin(r *http.Request) bool {
	if sameHost(r) {
		return true
	}
	origin := r.Header.Get("Origin")
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(hostname(parsed.Host), hostname(r.Host))
}

func sameHost(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

func hostname(value string) string {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		return value
	}
	return host
}

type client struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	done    chan struct{}
	code    string
	role    string
	token   string
}

type Handler struct {
	engine *Service
	store  *Store
	log    *log.Logger
	static http.Handler
	mu     sync.Mutex
	rooms  map[string][]*client
}

func NewHandler(engine *Service, logger *log.Logger, staticDir string) *Handler {
	return &Handler{
		engine: engine,
		log:    logger,
		static: http.FileServer(http.Dir(staticDir)),
		rooms:  map[string][]*client{},
	}
}

func (h *Handler) Mount(mux *http.ServeMux) {
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/ws", h.ws)
	mux.HandleFunc("/api/", h.api)
	mux.Handle("/", h.static)
}

func (h *Handler) ws(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.log.Printf("upgrade: %v", err)
		return
	}
	c := &client{conn: conn, done: make(chan struct{})}
	defer func() {
		h.unbind(c)
		close(c.done)
		_ = conn.Close()
	}()
	go c.ping()

	conn.SetReadLimit(128 << 10)
	_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	})
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.log.Printf("сокет: %v", err)
			}
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
		h.onMessage(c, data)
	}
}

type inbound struct {
	Op        string    `json:"op"`
	Title     string    `json:"title"`
	Category  string    `json:"category"`
	Rules     string    `json:"rules"`
	Seconds   int       `json:"seconds"`
	Preview   bool      `json:"preview"`
	QuizID    string    `json:"quizId"`
	Questions []rawJSON `json:"questions"`
	Code      string    `json:"code"`
	Token     string    `json:"token"`
	Name      string    `json:"name"`
	Option    *int      `json:"option"`
	Options   []int     `json:"options"`
}

type rawJSON struct {
	Text    string   `json:"text"`
	Image   string   `json:"image"`
	Multi   bool     `json:"multi"`
	Options []string `json:"options"`
	Correct *int     `json:"correct"`
	Answers []int    `json:"answers"`
}

func (h *Handler) onMessage(c *client, data []byte) {
	var msg inbound
	if err := json.Unmarshal(data, &msg); err != nil {
		c.send(errBody("Не понял сообщение"))
		return
	}
	switch msg.Op {
	case "create":
		h.create(c, msg)
	case "open":
		h.openSaved(c, msg)
	case "host":
		h.host(c, msg)
	case "join":
		h.join(c, msg)
	case "play":
		h.play(c, msg)
	case "watch":
		h.watch(c, msg)
	case "start":
		h.hostOp(c, h.engine.Start)
	case "reveal":
		h.hostOp(c, h.engine.Reveal)
	case "next":
		h.hostOp(c, h.engine.Advance)
	case "reset":
		h.hostOp(c, h.engine.Reset)
	case "answer":
		h.answer(c, msg)
	default:
		c.send(errBody("Неизвестная команда"))
	}
}

func (h *Handler) create(c *client, msg inbound) {
	raw := make([]RawQuestion, len(msg.Questions))
	for i, question := range msg.Questions {
		raw[i] = RawQuestion{
			Text:    question.Text,
			Image:   question.Image,
			Multi:   question.Multi,
			Options: question.Options,
			Correct: question.Correct,
			Answers: question.Answers,
		}
	}
	questions, err := ParseQuestions(raw)
	if err != nil {
		c.send(errBody(err.Error()))
		return
	}
	setup := Setup{
		Title:     msg.Title,
		Category:  msg.Category,
		Rules:     msg.Rules,
		Seconds:   msg.Seconds,
		Questions: questions,
	}
	if h.store != nil && !msg.Preview {
		user, err := h.store.UserByToken(msg.Token)
		if err != nil {
			c.send(errBody(err.Error()))
			return
		}
		saved, err := h.store.SaveQuiz(user, SavedQuiz{
			Title:     msg.Title,
			Category:  msg.Category,
			Rules:     msg.Rules,
			Seconds:   msg.Seconds,
			Questions: savedFromQuestions(questions),
		})
		if err != nil {
			c.send(errBody(err.Error()))
			return
		}
		setup.QuizID = saved.ID
		setup.HostUser = user.ID
		setup.Title = saved.Title
		setup.Category = saved.Category
		setup.Rules = saved.Rules
		setup.Seconds = saved.Seconds
	}
	code, token, err := h.engine.Create(setup)
	if err != nil {
		c.send(errBody(err.Error()))
		return
	}
	h.attach(c, code, "host", token)
	c.send(map[string]string{"type": "hello", "role": "host", "code": code, "token": token})
	h.broadcast(code)
	h.log.Printf("комната %s открыта", code)
}

func (h *Handler) openSaved(c *client, msg inbound) {
	if h.store == nil {
		c.send(errBody("База квизов недоступна"))
		return
	}
	user, err := h.store.UserByToken(msg.Token)
	if err != nil {
		c.send(errBody(err.Error()))
		return
	}
	saved, err := h.store.Quiz(user.ID, msg.QuizID)
	if err != nil {
		c.send(errBody(err.Error()))
		return
	}
	questions, err := questionsFromSaved(saved.Questions)
	if err != nil {
		c.send(errBody(err.Error()))
		return
	}
	code, token, err := h.engine.Create(Setup{
		Title:     saved.Title,
		Category:  saved.Category,
		Rules:     saved.Rules,
		Seconds:   saved.Seconds,
		QuizID:    saved.ID,
		HostUser:  user.ID,
		Questions: questions,
	})
	if err != nil {
		c.send(errBody(err.Error()))
		return
	}
	h.attach(c, code, "host", token)
	c.send(map[string]string{"type": "hello", "role": "host", "code": code, "token": token})
	h.broadcast(code)
	h.log.Printf("комната %s открыта из кабинета", code)
}

func (h *Handler) host(c *client, msg inbound) {
	if err := h.engine.ResumeHost(msg.Code, msg.Token); err != nil {
		c.send(errBody(err.Error()))
		return
	}
	code := codeKey(msg.Code)
	h.attach(c, code, "host", msg.Token)
	c.send(map[string]string{"type": "hello", "role": "host", "code": code, "token": msg.Token})
	h.broadcast(code)
}

func (h *Handler) join(c *client, msg inbound) {
	name := msg.Name
	userID := ""
	if msg.Token != "" && h.store != nil {
		user, err := h.store.UserByToken(msg.Token)
		if err != nil {
			c.send(errBody(err.Error()))
			return
		}
		name = user.Name
		userID = user.ID
	}
	token, err := h.engine.Join(msg.Code, name, userID)
	if err != nil {
		c.send(errBody(err.Error()))
		return
	}
	code := codeKey(msg.Code)
	label, _ := h.engine.ResumePlayer(code, token)
	h.attach(c, code, "player", token)
	c.send(map[string]string{"type": "hello", "role": "player", "code": code, "token": token, "name": label})
	h.broadcast(code)
}

func (h *Handler) play(c *client, msg inbound) {
	code := codeKey(msg.Code)
	name, err := h.engine.ResumePlayer(code, msg.Token)
	if err != nil {
		c.send(errBody(err.Error()))
		return
	}
	h.attach(c, code, "player", msg.Token)
	c.send(map[string]string{"type": "hello", "role": "player", "code": code, "token": msg.Token, "name": name})
	h.broadcast(code)
}

func (h *Handler) watch(c *client, msg inbound) {
	code := codeKey(msg.Code)
	if err := h.engine.Watch(code); err != nil {
		c.send(errBody(err.Error()))
		return
	}
	h.attach(c, code, "screen", "")
	c.send(map[string]string{"type": "hello", "role": "screen", "code": code})
	h.broadcast(code)
}

func (h *Handler) hostOp(c *client, fn func(code, token string) error) {
	if c.role != "host" || c.code == "" {
		c.send(errBody("Только организатор управляет квизом"))
		return
	}
	if err := fn(c.code, c.token); err != nil {
		c.send(errBody(err.Error()))
		return
	}
	h.broadcast(c.code)
	h.persist(c.code)
}

func (h *Handler) answer(c *client, msg inbound) {
	if c.role != "player" || c.code == "" {
		c.send(errBody("Отвечать может только участник"))
		return
	}
	picked := append([]int(nil), msg.Options...)
	if len(picked) == 0 && msg.Option != nil {
		picked = []int{*msg.Option}
	}
	if err := h.engine.Answer(c.code, c.token, picked); err != nil {
		c.send(errBody(err.Error()))
		return
	}
	h.broadcast(c.code)
}

func (h *Handler) attach(c *client, code, role, token string) {
	h.unbind(c)
	c.code = code
	c.role = role
	c.token = token
	h.mu.Lock()
	h.rooms[code] = append(h.rooms[code], c)
	h.mu.Unlock()
}

func (h *Handler) unbind(c *client) {
	if c.code == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.rooms[c.code]
	next := list[:0]
	for _, item := range list {
		if item != c {
			next = append(next, item)
		}
	}
	if len(next) == 0 {
		delete(h.rooms, c.code)
	} else {
		h.rooms[c.code] = next
	}
}

func (h *Handler) broadcast(code string) {
	h.mu.Lock()
	list := append([]*client(nil), h.rooms[code]...)
	h.mu.Unlock()
	for _, c := range list {
		state, err := h.engine.Snapshot(code, Viewer{Role: c.role, Token: c.token})
		if err != nil {
			continue
		}
		c.send(state)
	}
}

func (c *client) send(payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = c.conn.WriteMessage(websocket.TextMessage, data)
}

func (c *client) ping() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			c.writeMu.Lock()
			err := c.conn.WriteControl(websocket.PingMessage, []byte("ok"), time.Now().Add(5*time.Second))
			c.writeMu.Unlock()
			if err != nil {
				return
			}
		}
	}
}

func (h *Handler) persist(code string) {
	if h.store == nil {
		return
	}
	finished, ok := h.engine.TakeFinished(code)
	if !ok {
		return
	}
	if err := h.store.SavePlay(Play{
		QuizID:   finished.QuizID,
		HostID:   finished.HostUser,
		Code:     finished.Code,
		Title:    finished.Title,
		Category: finished.Category,
		When:     time.Now().Unix(),
		Rows:     finished.Rows,
	}); err != nil {
		h.log.Printf("история %s: %v", code, err)
	}
}

func (h *Handler) pulse() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for now := range ticker.C {
		for _, code := range h.engine.Pulse(now) {
			h.broadcast(code)
		}
	}
}

func errBody(message string) map[string]string {
	return map[string]string{"type": "error", "message": message}
}

func Serve(addr, staticDir string) error {
	logger := log.New(os.Stdout, "[квиз] ", log.LstdFlags)
	dataFile := os.Getenv("DATA_FILE")
	if dataFile == "" {
		dataFile = "data/quiz-store.json"
	}
	if err := os.MkdirAll(filepathDir(dataFile), 0o700); err != nil {
		return err
	}
	store, err := OpenStore(dataFile)
	if err != nil {
		return err
	}
	handler := NewHandler(New(), logger, staticDir)
	handler.store = store
	go handler.pulse()
	mux := http.NewServeMux()
	handler.Mount(mux)
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	logger.Printf("слушаю %s", addr)
	return server.ListenAndServe()
}

func filepathDir(path string) string {
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return "."
	}
	return path[:i]
}
