package quiz

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func sampleQuestions() []Question {
	return []Question{{
		Text:    "Какой протокол держит комнату?",
		Options: []string{"HTTP", "WebSocket"},
		Correct: []int{1},
	}}
}

func demoSetup() Setup {
	return Setup{Title: "Демо", Questions: sampleQuestions()}
}

func TestPlayerDoesNotSeeTheAnswerEarly(t *testing.T) {
	svc := New()
	code, host, err := svc.Create(demoSetup())
	if err != nil {
		t.Fatal(err)
	}
	player, err := svc.Join(strings.ToLower(code), "Аня", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(code, host); err != nil {
		t.Fatal(err)
	}
	state, err := svc.Snapshot(code, Viewer{Role: "player", Token: player})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(state)
	if strings.Contains(string(raw), host) {
		t.Fatal("host token leaked into the player snapshot")
	}
	if state.Question == nil || state.Question.Correct != nil || state.Question.Counts != nil {
		t.Fatalf("answer leaked: %+v", state.Question)
	}
	if err := svc.Answer(code, player, []int{1}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Answer(code, player, []int{0}); err != errAnswered {
		t.Fatalf("second answer: %v", err)
	}
	if err := svc.Reveal(code, host); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reveal(code, host); err != nil {
		t.Fatal(err)
	}
	state, err = svc.Snapshot(code, Viewer{Role: "player", Token: player})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Question.Correct) != 1 || state.Question.Correct[0] != 1 {
		t.Fatalf("correct after reveal: %+v", state.Question)
	}
	if state.You.Score < 100 || state.You.Score > 150 {
		t.Fatalf("score = %d", state.You.Score)
	}
	again := state.You.Score
	if err := svc.Reveal(code, "nope"); err != errHost {
		t.Fatalf("foreign reveal: %v", err)
	}
	state, _ = svc.Snapshot(code, Viewer{Role: "player", Token: player})
	if state.You.Score != again {
		t.Fatalf("score changed after rejected reveal: %d", state.You.Score)
	}
}

func TestWrongAnswerScoresNothing(t *testing.T) {
	svc := New()
	code, host, err := svc.Create(Setup{Questions: sampleQuestions()})
	if err != nil {
		t.Fatal(err)
	}
	player, err := svc.Join(code, "Боря", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(code, host); err != nil {
		t.Fatal(err)
	}
	if err := svc.Answer(code, player, []int{0}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reveal(code, host); err != nil {
		t.Fatal(err)
	}
	state, err := svc.Snapshot(code, Viewer{Role: "host", Token: host})
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Players) != 1 || state.Players[0].Score != 0 {
		t.Fatalf("%+v", state.Players)
	}
	if state.Question.Counts[0] != 1 || state.Question.Counts[1] != 0 {
		t.Fatalf("counts %+v", state.Question.Counts)
	}
}

func TestMultipleChoiceNeedsTheWholeSet(t *testing.T) {
	svc := New()
	code, host, err := svc.Create(Setup{Seconds: 20, Questions: []Question{{
		Text:    "Что держит живой квиз?",
		Multi:   true,
		Options: []string{"WebSocket", "FTP", "Комната"},
		Correct: []int{0, 2},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	player, err := svc.Join(code, "Илья", "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(code, host); err != nil {
		t.Fatal(err)
	}
	if err := svc.Answer(code, player, []int{0}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Reveal(code, host); err != nil {
		t.Fatal(err)
	}
	state, err := svc.Snapshot(code, Viewer{Role: "player", Token: player})
	if err != nil {
		t.Fatal(err)
	}
	if state.You.Score != 0 {
		t.Fatalf("partial set scored %d", state.You.Score)
	}
}

func TestTimerClosesTheQuestion(t *testing.T) {
	svc := New()
	code, host, err := svc.Create(Setup{Seconds: 10, Questions: sampleQuestions()})
	if err != nil {
		t.Fatal(err)
	}
	player, err := svc.Join(code, "Аня", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Start(code, host); err != nil {
		t.Fatal(err)
	}
	svc.mu.Lock()
	svc.rooms[code].Deadline = time.Now().Add(-time.Second)
	svc.mu.Unlock()
	if len(svc.Pulse(time.Now())) != 1 {
		t.Fatal("timer room was not pulsed")
	}
	if err := svc.Answer(code, player, []int{1}); err != errClosed {
		t.Fatalf("answer after timer: %v", err)
	}
	state, err := svc.Snapshot(code, Viewer{Role: "player", Token: player})
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != "reveal" {
		t.Fatalf("phase %s", state.Phase)
	}
}

func TestParseRejectsEmptyOption(t *testing.T) {
	zero := 0
	_, err := ParseQuestions([]RawQuestion{{
		Text:    "Вопрос",
		Options: []string{"да", " "},
		Correct: &zero,
	}})
	if err != errEmptyOpt {
		t.Fatalf("%v", err)
	}
}
