package quiz

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	alphabet     = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	maxRooms     = 40
	maxPlayers   = 80
	maxQuestions = 20
	roomTTL      = 6 * time.Hour
)

type Error string

func (e Error) Error() string { return string(e) }

const (
	errQuestions = Error("Нужно от 1 до 20 вопросов")
	errShortQ    = Error("Вопрос слишком короткий")
	errOptions   = Error("У вопроса должно быть 2–4 варианта")
	errEmptyOpt  = Error("Пустой вариант ответа")
	errDupOpt    = Error("Варианты не должны повторяться")
	errCorrect   = Error("Отметьте верный ответ")
	errNoRoom    = Error("Комната не найдена")
	errPick      = Error("Отметьте хотя бы один вариант")
	errFull      = Error("Комната уже полная")
	errName      = Error("Введите имя, хотя бы 2 буквы")
	errSession   = Error("Сессия участника устарела, войдите снова")
	errHost      = Error("Только организатор управляет квизом")
	errStarted   = Error("Квиз уже запущен")
	errClosed    = Error("Сейчас нельзя отвечать")
	errAnswered  = Error("Ответ уже принят")
	errOption    = Error("Нет такого варианта")
	errNothing   = Error("Сейчас нечего показывать")
	errReveal    = Error("Сначала покажите ответ")
	errCode      = Error("Не удалось открыть комнату, попробуйте ещё раз")
)

type Question struct {
	Text    string
	Image   string
	Multi   bool
	Options []string
	Correct []int
}

type Player struct {
	Token      string
	UserID     string
	Name       string
	Score      int
	Answer     []int
	AnsweredAt time.Time
	Gain       int
}

type Room struct {
	Code      string
	Title     string
	Category  string
	Rules     string
	Seconds   int
	QuizID    string
	HostUser  string
	HostToken string
	Questions []Question
	Phase     string
	Index     int
	Players   map[string]*Player
	Started   time.Time
	Deadline  time.Time
	Awarded   int
	Saved     bool
	Seen      time.Time
}

type Setup struct {
	Title     string
	Category  string
	Rules     string
	QuizID    string
	HostUser  string
	Seconds   int
	Questions []Question
}

type Service struct {
	mu    sync.Mutex
	rooms map[string]*Room
}

func New() *Service {
	return &Service{rooms: map[string]*Room{}}
}

type QuestionView struct {
	Index      int      `json:"index"`
	Total      int      `json:"total"`
	Text       string   `json:"text"`
	Image      string   `json:"image,omitempty"`
	Multi      bool     `json:"multi,omitempty"`
	Options    []string `json:"options"`
	Counts     []int    `json:"counts,omitempty"`
	Correct    []int    `json:"correct,omitempty"`
	YourAnswer []int    `json:"yourAnswer,omitempty"`
}

type PlayerView struct {
	Name     string `json:"name"`
	Score    int    `json:"score"`
	Answered bool   `json:"answered"`
	Gain     int    `json:"gain,omitempty"`
}

type YouView struct {
	Name  string `json:"name"`
	Score int    `json:"score"`
	Gain  int    `json:"gain,omitempty"`
}

type State struct {
	Type          string        `json:"type"`
	Role          string        `json:"role"`
	Code          string        `json:"code"`
	Title         string        `json:"title"`
	Category      string        `json:"category,omitempty"`
	Rules         string        `json:"rules,omitempty"`
	Phase         string        `json:"phase"`
	Timed         bool          `json:"timed,omitempty"`
	SecondsLeft   int           `json:"secondsLeft,omitempty"`
	QuestionCount int           `json:"questionCount"`
	Question      *QuestionView `json:"question,omitempty"`
	Players       []PlayerView  `json:"players"`
	You           *YouView      `json:"you,omitempty"`
	Answered      int           `json:"answered"`
	TotalPlayers  int           `json:"totalPlayers"`
	Winners       []string      `json:"winners,omitempty"`
}

type Viewer struct {
	Role  string
	Token string
}

func (s *Service) Create(setup Setup) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(time.Now())
	if len(setup.Questions) < 1 || len(setup.Questions) > maxQuestions {
		return "", "", errQuestions
	}
	if len(s.rooms) >= maxRooms {
		var oldest *Room
		for _, room := range s.rooms {
			if oldest == nil || room.Seen.Before(oldest.Seen) {
				oldest = room
			}
		}
		if oldest != nil {
			delete(s.rooms, oldest.Code)
		}
	}
	code, err := s.freshCodeLocked()
	if err != nil {
		return "", "", err
	}
	name := clean(setup.Title, 60)
	if name == "" {
		name = "Квиз"
	}
	category := clean(setup.Category, 40)
	if category == "" {
		category = "Общее"
	}
	token := newToken()
	s.rooms[code] = &Room{
		Code:      code,
		Title:     name,
		Category:  category,
		Rules:     clean(setup.Rules, 240),
		Seconds:   allowedSeconds(setup.Seconds),
		QuizID:    setup.QuizID,
		HostUser:  setup.HostUser,
		HostToken: token,
		Questions: cloneQuestions(setup.Questions),
		Phase:     "lobby",
		Index:     -1,
		Players:   map[string]*Player{},
		Awarded:   -1,
		Seen:      time.Now(),
	}
	return code, token, nil
}

func (s *Service) Join(code, name, userID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	room, err := s.roomLocked(code)
	if err != nil {
		return "", err
	}
	if userID != "" {
		for _, player := range room.Players {
			if player.UserID == userID {
				room.Seen = time.Now()
				return player.Token, nil
			}
		}
	}
	if len(room.Players) >= maxPlayers {
		return "", errFull
	}
	label := clean(name, 24)
	if len([]rune(label)) < 2 {
		return "", errName
	}
	token := newToken()
	room.Players[token] = &Player{Token: token, UserID: userID, Name: label}
	room.Seen = time.Now()
	return token, nil
}

func (s *Service) ResumeHost(code, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	room, err := s.roomLocked(code)
	if err != nil {
		return err
	}
	if !same(token, room.HostToken) {
		return errNoRoom
	}
	room.Seen = time.Now()
	return nil
}

func (s *Service) ResumePlayer(code, token string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	room, err := s.roomLocked(code)
	if err != nil {
		return "", err
	}
	player := room.Players[token]
	if player == nil || !same(token, player.Token) {
		return "", errSession
	}
	room.Seen = time.Now()
	return player.Name, nil
}

func (s *Service) Watch(code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	room, err := s.roomLocked(code)
	if err != nil {
		return err
	}
	room.Seen = time.Now()
	return nil
}

func (s *Service) Start(code, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	room, err := s.hostLocked(code, token)
	if err != nil {
		return err
	}
	if room.Phase != "lobby" {
		return errStarted
	}
	room.Phase = "question"
	room.Index = 0
	room.Awarded = -1
	room.Saved = false
	room.Started = time.Now()
	room.Deadline = deadlineFrom(room.Started, room.Seconds)
	for _, player := range room.Players {
		player.Answer = nil
		player.Gain = 0
		player.Score = 0
	}
	room.Seen = time.Now()
	return nil
}

func (s *Service) Answer(code, token string, picked []int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	room, err := s.roomLocked(code)
	if err != nil {
		return err
	}
	if room.Phase != "question" || questionClosed(room, time.Now()) {
		return errClosed
	}
	player := room.Players[token]
	if player == nil {
		return errSession
	}
	if len(player.Answer) > 0 {
		return errAnswered
	}
	normalized, err := normalizePick(room.Questions[room.Index], picked)
	if err != nil {
		return err
	}
	player.Answer = normalized
	player.AnsweredAt = time.Now()
	room.Seen = time.Now()
	return nil
}

func (s *Service) Reveal(code, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	room, err := s.hostLocked(code, token)
	if err != nil {
		return err
	}
	if room.Phase != "question" && room.Phase != "reveal" {
		return errNothing
	}
	if room.Phase == "reveal" {
		return nil
	}
	revealLocked(room)
	return nil
}

func (s *Service) Advance(code, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	room, err := s.hostLocked(code, token)
	if err != nil {
		return err
	}
	if room.Phase != "reveal" {
		return errReveal
	}
	if room.Index+1 >= len(room.Questions) {
		room.Phase = "done"
		room.Seen = time.Now()
		return nil
	}
	room.Index++
	room.Phase = "question"
	room.Started = time.Now()
	room.Deadline = deadlineFrom(room.Started, room.Seconds)
	for _, player := range room.Players {
		player.Answer = nil
		player.Gain = 0
	}
	room.Seen = time.Now()
	return nil
}

func (s *Service) Reset(code, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	room, err := s.hostLocked(code, token)
	if err != nil {
		return err
	}
	room.Phase = "lobby"
	room.Index = -1
	room.Awarded = -1
	room.Saved = false
	for _, player := range room.Players {
		player.Answer = nil
		player.Gain = 0
		player.Score = 0
	}
	room.Seen = time.Now()
	return nil
}

func (s *Service) Snapshot(code string, viewer Viewer) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	room, err := s.roomLocked(code)
	if err != nil {
		return State{}, err
	}
	var self *Player
	if viewer.Role == "player" {
		self = room.Players[viewer.Token]
		if self == nil {
			return State{}, errSession
		}
	}
	state := State{
		Type:          "state",
		Role:          viewer.Role,
		Code:          room.Code,
		Title:         room.Title,
		Category:      room.Category,
		Rules:         room.Rules,
		Phase:         room.Phase,
		QuestionCount: len(room.Questions),
		Players:       []PlayerView{},
	}
	if room.Phase == "question" && room.Seconds > 0 {
		state.Timed = true
		left := int(time.Until(room.Deadline).Seconds())
		if left < 0 {
			left = 0
		}
		state.SecondsLeft = left
	}
	if room.Phase != "lobby" && room.Index >= 0 && room.Index < len(room.Questions) {
		current := room.Questions[room.Index]
		showKey := room.Phase == "reveal" || room.Phase == "done"
		view := &QuestionView{
			Index:   room.Index,
			Total:   len(room.Questions),
			Text:    current.Text,
			Image:   current.Image,
			Multi:   current.Multi,
			Options: append([]string(nil), current.Options...),
		}
		if viewer.Role != "player" || showKey {
			counts := make([]int, len(current.Options))
			for _, player := range room.Players {
				for _, picked := range player.Answer {
					if picked >= 0 && picked < len(counts) {
						counts[picked]++
					}
				}
			}
			view.Counts = counts
		}
		if showKey {
			view.Correct = append([]int(nil), current.Correct...)
		}
		if self != nil && len(self.Answer) > 0 {
			view.YourAnswer = append([]int(nil), self.Answer...)
		}
		state.Question = view
	}
	list := make([]*Player, 0, len(room.Players))
	for _, player := range room.Players {
		list = append(list, player)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Score != list[j].Score {
			return list[i].Score > list[j].Score
		}
		return list[i].Name < list[j].Name
	})
	showGain := room.Phase == "reveal" || room.Phase == "done"
	for _, player := range list {
		item := PlayerView{
			Name:     player.Name,
			Score:    player.Score,
			Answered: room.Phase == "question" && len(player.Answer) > 0,
		}
		if item.Answered {
			state.Answered++
		}
		if showGain {
			item.Gain = player.Gain
		}
		state.Players = append(state.Players, item)
	}
	state.TotalPlayers = len(list)
	if self != nil {
		you := &YouView{Name: self.Name, Score: self.Score}
		if showGain {
			you.Gain = self.Gain
		}
		state.You = you
	}
	if room.Phase == "done" && len(list) > 0 {
		top := list[0].Score
		for _, player := range list {
			if player.Score == top {
				state.Winners = append(state.Winners, player.Name)
			}
		}
	}
	return state, nil
}

func ParseQuestions(raw []RawQuestion) ([]Question, error) {
	if len(raw) < 1 || len(raw) > maxQuestions {
		return nil, errQuestions
	}
	out := make([]Question, 0, len(raw))
	for _, item := range raw {
		text := clean(item.Text, 180)
		if len([]rune(text)) < 3 {
			return nil, errShortQ
		}
		if len(item.Options) < 2 || len(item.Options) > 4 {
			return nil, errOptions
		}
		options := make([]string, 0, len(item.Options))
		seen := map[string]bool{}
		for _, option := range item.Options {
			label := clean(option, 60)
			if label == "" {
				return nil, errEmptyOpt
			}
			key := strings.ToLower(label)
			if seen[key] {
				return nil, errDupOpt
			}
			seen[key] = true
			options = append(options, label)
		}
		image, err := cleanImage(item.Image)
		if err != nil {
			return nil, err
		}
		answers := append([]int(nil), item.Answers...)
		if len(answers) == 0 && item.Correct != nil {
			answers = []int{*item.Correct}
		}
		question := Question{Text: text, Image: image, Multi: item.Multi, Options: options}
		normalized, err := normalizePick(question, answers)
		if err != nil {
			return nil, errCorrect
		}
		if !item.Multi && len(normalized) != 1 {
			return nil, errCorrect
		}
		question.Correct = normalized
		out = append(out, question)
	}
	return out, nil
}

type RawQuestion struct {
	Text    string
	Image   string
	Multi   bool
	Options []string
	Correct *int
	Answers []int
}

func (s *Service) hostLocked(code, token string) (*Room, error) {
	room, err := s.roomLocked(code)
	if err != nil {
		return nil, err
	}
	if !same(token, room.HostToken) {
		return nil, errHost
	}
	return room, nil
}

func (s *Service) roomLocked(code string) (*Room, error) {
	room := s.rooms[codeKey(code)]
	if room == nil {
		return nil, errNoRoom
	}
	return room, nil
}

func (s *Service) sweepLocked(now time.Time) {
	for code, room := range s.rooms {
		if now.Sub(room.Seen) > roomTTL {
			delete(s.rooms, code)
		}
	}
}

func (s *Service) freshCodeLocked() (string, error) {
	buf := make([]byte, 4)
	for try := 0; try < 20; try++ {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		var b strings.Builder
		for _, x := range buf {
			b.WriteByte(alphabet[int(x)%len(alphabet)])
		}
		code := b.String()
		if _, ok := s.rooms[code]; !ok {
			return code, nil
		}
	}
	return "", errCode
}

func cloneQuestions(questions []Question) []Question {
	out := make([]Question, len(questions))
	for i, question := range questions {
		out[i] = Question{
			Text:    question.Text,
			Image:   question.Image,
			Multi:   question.Multi,
			Options: append([]string(nil), question.Options...),
			Correct: append([]int(nil), question.Correct...),
		}
	}
	return out
}

func pointsFor(elapsed, window time.Duration) int {
	if window <= 0 {
		window = 20 * time.Second
	}
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > window {
		elapsed = window
	}
	bonus := int(math.Round(50 * (1 - elapsed.Seconds()/window.Seconds())))
	if bonus < 0 {
		bonus = 0
	}
	return 100 + bonus
}

func deadlineFrom(start time.Time, seconds int) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return start.Add(time.Duration(seconds) * time.Second)
}

func questionClosed(room *Room, now time.Time) bool {
	return room.Seconds > 0 && !room.Deadline.IsZero() && !now.Before(room.Deadline)
}

func revealLocked(room *Room) {
	question := room.Questions[room.Index]
	window := time.Duration(room.Seconds) * time.Second
	for _, player := range room.Players {
		player.Gain = 0
		if sameSet(player.Answer, question.Correct) {
			player.Gain = pointsFor(player.AnsweredAt.Sub(room.Started), window)
			player.Score += player.Gain
		}
	}
	room.Awarded = room.Index
	room.Phase = "reveal"
	room.Seen = time.Now()
}

func normalizePick(question Question, picked []int) ([]int, error) {
	if len(picked) == 0 {
		return nil, errPick
	}
	if !question.Multi && len(picked) != 1 {
		return nil, errOption
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(picked))
	for _, n := range picked {
		if n < 0 || n >= len(question.Options) || seen[n] {
			return nil, errOption
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Ints(out)
	return out, nil
}

func sameSet(got, want []int) bool {
	if len(got) == 0 || len(got) != len(want) {
		return false
	}
	a := append([]int(nil), got...)
	b := append([]int(nil), want...)
	sort.Ints(a)
	sort.Ints(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cleanImage(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "data:image/") {
		if len(raw) > 24000 || strings.ContainsAny(raw, "\n\r") {
			return "", errImage
		}
		return raw, nil
	}
	if len(raw) > 300 {
		return "", errImage
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return "", errImage
	}
	return raw, nil
}

// Pulse закрывает вопрос, когда вышло время, и возвращает комнаты с таймером.
func (s *Service) Pulse(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var codes []string
	for code, room := range s.rooms {
		if room.Phase != "question" || room.Seconds <= 0 {
			continue
		}
		if questionClosed(room, now) {
			revealLocked(room)
		}
		codes = append(codes, code)
	}
	return codes
}

type Finished struct {
	QuizID   string
	HostUser string
	Code     string
	Title    string
	Category string
	Rows     []PlayRow
}

// TakeFinished отдаёт итог один раз, когда квиз дошёл до таблицы.
func (s *Service) TakeFinished(code string) (Finished, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	room := s.rooms[codeKey(code)]
	if room == nil || room.Phase != "done" || room.Saved {
		return Finished{}, false
	}
	room.Saved = true
	list := make([]*Player, 0, len(room.Players))
	for _, player := range room.Players {
		list = append(list, player)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Score != list[j].Score {
			return list[i].Score > list[j].Score
		}
		return list[i].Name < list[j].Name
	})
	rows := make([]PlayRow, 0, len(list))
	place := 0
	last := -1
	for i, player := range list {
		if i == 0 || player.Score != last {
			place = i + 1
			last = player.Score
		}
		rows = append(rows, PlayRow{
			UserID: player.UserID,
			Name:   player.Name,
			Score:  player.Score,
			Place:  place,
		})
	}
	return Finished{
		QuizID:   room.QuizID,
		HostUser: room.HostUser,
		Code:     room.Code,
		Title:    room.Title,
		Category: room.Category,
		Rows:     rows,
	}, true
}

func clean(value string, limit int) string {
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return strings.TrimSpace(value)
}

func newToken() string {
	buf := make([]byte, 18)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

func codeKey(code string) string {
	code = clean(code, 8)
	return strings.ToUpper(strings.Join(strings.Fields(code), ""))
}

func same(a, b string) bool {
	if a == "" || len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
