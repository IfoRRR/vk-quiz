-- Модель базы квиза. Рабочий процесс хранит те же сущности
-- в файле data/quiz-store.json: сервис ограничен по памяти,
-- а схема остаётся табличной.

CREATE TABLE users (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    email         TEXT NOT NULL UNIQUE,
    role          TEXT NOT NULL CHECK (role IN ('organizer', 'player')),
    password_hash TEXT NOT NULL,
    salt          TEXT NOT NULL,
    created_at    INTEGER NOT NULL
);

CREATE TABLE sessions (
    token   TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES users(id)
);

CREATE TABLE quizzes (
    id         TEXT PRIMARY KEY,
    owner_id   TEXT NOT NULL REFERENCES users(id),
    title      TEXT NOT NULL,
    category   TEXT NOT NULL,
    seconds    INTEGER NOT NULL,
    rules      TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

CREATE TABLE questions (
    id       INTEGER PRIMARY KEY,
    quiz_id  TEXT NOT NULL REFERENCES quizzes(id),
    position INTEGER NOT NULL,
    prompt   TEXT NOT NULL,
    image    TEXT,
    multi    INTEGER NOT NULL,
    options  TEXT NOT NULL,
    correct  TEXT NOT NULL
);

CREATE TABLE plays (
    id       TEXT PRIMARY KEY,
    quiz_id  TEXT,
    host_id  TEXT REFERENCES users(id),
    code     TEXT NOT NULL,
    title    TEXT NOT NULL,
    category TEXT,
    played_at INTEGER NOT NULL
);

CREATE TABLE play_results (
    play_id TEXT NOT NULL REFERENCES plays(id),
    user_id TEXT,
    name    TEXT NOT NULL,
    score   INTEGER NOT NULL,
    place   INTEGER NOT NULL
);
