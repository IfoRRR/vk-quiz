package quiz

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	errEmail    = Error("Укажите почту")
	errPassword = Error("Пароль — хотя бы 6 символов")
	errRole     = Error("Выберите роль: организатор или участник")
	errTaken    = Error("Такая почта уже зарегистрирована")
	errLogin    = Error("Почта или пароль не сходятся")
	errAuth     = Error("Сначала войдите в кабинет")
	errOwn      = Error("Этот квиз принадлежит другому организатору")
	errImage    = Error("Картинка должна быть ссылкой http или https")
)

type User struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	Role    string `json:"role"`
	Pass    string `json:"pass"`
	Salt    string `json:"salt"`
	Created int64  `json:"created"`
}

type PublicUser struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Role  string `json:"role"`
}

type SavedQuestion struct {
	Text    string   `json:"text"`
	Image   string   `json:"image,omitempty"`
	Multi   bool     `json:"multi"`
	Options []string `json:"options"`
	Correct []int    `json:"correct"`
}

type SavedQuiz struct {
	ID        string          `json:"id"`
	Owner     string          `json:"owner"`
	Title     string          `json:"title"`
	Category  string          `json:"category"`
	Seconds   int             `json:"seconds"`
	Rules     string          `json:"rules"`
	Questions []SavedQuestion `json:"questions"`
	Created   int64           `json:"created"`
}

type PlayRow struct {
	UserID string `json:"userId,omitempty"`
	Name   string `json:"name"`
	Score  int    `json:"score"`
	Place  int    `json:"place"`
}

type Play struct {
	ID       string    `json:"id"`
	QuizID   string    `json:"quizId,omitempty"`
	HostID   string    `json:"hostId,omitempty"`
	Code     string    `json:"code"`
	Title    string    `json:"title"`
	Category string    `json:"category,omitempty"`
	When     int64     `json:"when"`
	Rows     []PlayRow `json:"rows"`
}

type sessionRow struct {
	Token  string `json:"token"`
	UserID string `json:"userId"`
}

type diskFile struct {
	Users    []User       `json:"users"`
	Sessions []sessionRow `json:"sessions"`
	Quizzes  []SavedQuiz  `json:"quizzes"`
	Plays    []Play       `json:"plays"`
}

// Store — файловая база: пользователи, квизы и история.
// Схема таблиц описана в schema.sql. Процесс держит копию в памяти
// и переписывает файл целиком, чтобы уложиться в лимит памяти сервиса.
type Store struct {
	mu   sync.Mutex
	path string
	disk diskFile
}

func OpenStore(path string) (*Store, error) {
	store := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, store.flushLocked()
		}
		return nil, err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &store.disk); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (s *Store) Register(name, email, password, role string) (PublicUser, string, error) {
	label := clean(name, 24)
	if len([]rune(label)) < 2 {
		return PublicUser{}, "", errName
	}
	mail := normalizeEmail(email)
	if mail == "" {
		return PublicUser{}, "", errEmail
	}
	if len(password) < 6 || len(password) > 72 {
		return PublicUser{}, "", errPassword
	}
	if role != "organizer" && role != "player" {
		return PublicUser{}, "", errRole
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.disk.Users) >= 300 {
		return PublicUser{}, "", Error("Кабинет сейчас не принимает новые регистрации")
	}
	for _, user := range s.disk.Users {
		if user.Email == mail {
			return PublicUser{}, "", errTaken
		}
	}
	salt := newToken()
	user := User{
		ID:      newToken()[:12],
		Name:    label,
		Email:   mail,
		Role:    role,
		Salt:    salt,
		Pass:    stretch(password, salt),
		Created: time.Now().Unix(),
	}
	s.disk.Users = append(s.disk.Users, user)
	token := s.bindLocked(user.ID)
	if err := s.flushLocked(); err != nil {
		return PublicUser{}, "", err
	}
	return user.public(), token, nil
}

func (s *Store) Login(email, password string) (PublicUser, string, error) {
	mail := normalizeEmail(email)
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.disk.Users {
		if user.Email != mail {
			continue
		}
		if subtlePass(user.Pass, stretch(password, user.Salt)) {
			token := s.bindLocked(user.ID)
			if err := s.flushLocked(); err != nil {
				return PublicUser{}, "", err
			}
			return user.public(), token, nil
		}
		return PublicUser{}, "", errLogin
	}
	return PublicUser{}, "", errLogin
}

func (s *Store) UserByToken(token string) (PublicUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := ""
	for _, row := range s.disk.Sessions {
		if same(row.Token, token) {
			id = row.UserID
			break
		}
	}
	if id == "" {
		return PublicUser{}, errAuth
	}
	for _, user := range s.disk.Users {
		if user.ID == id {
			return user.public(), nil
		}
	}
	return PublicUser{}, errAuth
}

func (s *Store) SaveQuiz(owner PublicUser, quiz SavedQuiz) (SavedQuiz, error) {
	if owner.Role != "organizer" {
		return SavedQuiz{}, Error("Создавать квизы может организатор")
	}
	quiz.Title = clean(quiz.Title, 60)
	if quiz.Title == "" {
		quiz.Title = "Квиз"
	}
	quiz.Category = clean(quiz.Category, 40)
	if quiz.Category == "" {
		quiz.Category = "Общее"
	}
	quiz.Rules = clean(quiz.Rules, 240)
	quiz.Seconds = allowedSeconds(quiz.Seconds)
	questions, err := questionsFromSaved(quiz.Questions)
	if err != nil {
		return SavedQuiz{}, err
	}
	quiz.Questions = savedFromQuestions(questions)
	quiz.Owner = owner.ID
	quiz.ID = newToken()[:12]
	quiz.Created = time.Now().Unix()
	s.mu.Lock()
	defer s.mu.Unlock()
	owned := 0
	for _, item := range s.disk.Quizzes {
		if item.Owner == owner.ID {
			owned++
		}
	}
	if owned >= 40 {
		return SavedQuiz{}, Error("В кабинете уже 40 квизов")
	}
	s.disk.Quizzes = append(s.disk.Quizzes, quiz)
	if err := s.flushLocked(); err != nil {
		return SavedQuiz{}, err
	}
	return quiz, nil
}

func (s *Store) Quiz(ownerID, id string) (SavedQuiz, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, quiz := range s.disk.Quizzes {
		if quiz.ID == id {
			if quiz.Owner != ownerID {
				return SavedQuiz{}, errOwn
			}
			return quiz, nil
		}
	}
	return SavedQuiz{}, errNoRoom
}

func (s *Store) Quizzes(ownerID string) []SavedQuiz {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []SavedQuiz{}
	for i := len(s.disk.Quizzes) - 1; i >= 0; i-- {
		if s.disk.Quizzes[i].Owner == ownerID {
			out = append(out, s.disk.Quizzes[i])
		}
	}
	return out
}

func (s *Store) SavePlay(play Play) error {
	if play.Code == "" {
		return nil
	}
	play.ID = newToken()[:12]
	if play.When == 0 {
		play.When = time.Now().Unix()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.disk.Plays = append(s.disk.Plays, play)
	if len(s.disk.Plays) > 200 {
		s.disk.Plays = s.disk.Plays[len(s.disk.Plays)-200:]
	}
	return s.flushLocked()
}

func (s *Store) History(user PublicUser) (hosted []Play, played []Play) {
	s.mu.Lock()
	defer s.mu.Unlock()
	hosted, played = []Play{}, []Play{}
	for i := len(s.disk.Plays) - 1; i >= 0; i-- {
		play := s.disk.Plays[i]
		if play.HostID == user.ID {
			hosted = append(hosted, play)
		}
		for _, row := range play.Rows {
			if row.UserID == user.ID {
				played = append(played, play)
				break
			}
		}
	}
	return hosted, played
}

func (s *Store) bindLocked(userID string) string {
	token := newToken()
	s.disk.Sessions = append(s.disk.Sessions, sessionRow{Token: token, UserID: userID})
	if len(s.disk.Sessions) > 400 {
		s.disk.Sessions = s.disk.Sessions[len(s.disk.Sessions)-400:]
	}
	return token
}

func (s *Store) flushLocked() error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(s.disk, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (u User) public() PublicUser {
	return PublicUser{ID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role}
}

func normalizeEmail(value string) string {
	value = strings.ToLower(clean(value, 80))
	if !strings.Contains(value, "@") || strings.Contains(value, " ") {
		return ""
	}
	parts := strings.Split(value, "@")
	if len(parts) != 2 || parts[0] == "" || !strings.Contains(parts[1], ".") {
		return ""
	}
	return value
}

func stretch(password, salt string) string {
	sum := sha256.Sum256([]byte(salt + "\n" + password))
	for i := 0; i < 20000; i++ {
		sum = sha256.Sum256(sum[:])
	}
	return hex.EncodeToString(sum[:])
}

func subtlePass(stored, got string) bool {
	if len(stored) != len(got) || stored == "" {
		return false
	}
	return same(stored, got)
}

func allowedSeconds(value int) int {
	switch value {
	case 0, 10, 15, 20, 30, 45, 60:
		return value
	default:
		return 20
	}
}

func questionsFromSaved(raw []SavedQuestion) ([]Question, error) {
	items := make([]RawQuestion, len(raw))
	for i, question := range raw {
		items[i] = RawQuestion{
			Text:    question.Text,
			Image:   question.Image,
			Multi:   question.Multi,
			Options: question.Options,
			Answers: question.Correct,
		}
	}
	return ParseQuestions(items)
}

func savedFromQuestions(questions []Question) []SavedQuestion {
	out := make([]SavedQuestion, len(questions))
	for i, question := range questions {
		out[i] = SavedQuestion{
			Text:    question.Text,
			Image:   question.Image,
			Multi:   question.Multi,
			Options: append([]string(nil), question.Options...),
			Correct: append([]int(nil), question.Correct...),
		}
	}
	return out
}
