package quiz

import (
	"path/filepath"
	"testing"
)

func TestAccountsAndHistory(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "quiz.json"))
	if err != nil {
		t.Fatal(err)
	}
	host, token, err := store.Register("Катя", "Katya@Example.com", "secret1", "organizer")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Register("Катя", "katya@example.com", "secret1", "organizer"); err != errTaken {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := store.UserByToken(token); err != nil {
		t.Fatal(err)
	}
	again, _, err := store.Login("katya@example.com", "secret1")
	if err != nil || again.ID != host.ID || again.Role != "organizer" {
		t.Fatalf("login %+v %v", again, err)
	}
	if _, _, err := store.Login("katya@example.com", "nope12"); err != errLogin {
		t.Fatalf("bad password: %v", err)
	}
	player, _, err := store.Register("Илья", "ilya@example.com", "secret1", "player")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveQuiz(player, SavedQuiz{
		Title: "Чужой",
		Questions: []SavedQuestion{{
			Text: "Вопрос достаточно длинный", Options: []string{"а", "б"}, Correct: []int{0},
		}},
	}); err == nil {
		t.Fatal("player saved a quiz")
	}
	saved, err := store.SaveQuiz(host, SavedQuiz{
		Title:    "Зал",
		Category: "IT",
		Seconds:  20,
		Rules:    "Пока вопрос на экране.",
		Questions: []SavedQuestion{{
			Text:    "Что на картинке?",
			Image:   "https://example.com/a.png",
			Multi:   true,
			Options: []string{"Квиз", "Почта", "Код"},
			Correct: []int{0, 2},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Questions[0].Multi != true || saved.Seconds != 20 {
		t.Fatalf("%+v", saved)
	}
	if err := store.SavePlay(Play{
		QuizID: saved.ID, HostID: host.ID, Code: "ABCD", Title: saved.Title,
		Rows: []PlayRow{{UserID: player.ID, Name: player.Name, Score: 120, Place: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	hosted, played := store.History(host)
	if len(hosted) != 1 || len(played) != 0 {
		t.Fatalf("host history %d %d", len(hosted), len(played))
	}
	hosted, played = store.History(player)
	if len(played) != 1 || played[0].Rows[0].Place != 1 || len(hosted) != 0 {
		t.Fatalf("player history %+v", played)
	}
}
