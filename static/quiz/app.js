const LETTERS = ["А", "Б", "В", "Г"];

function emptyQuestion() {
  return { text: "", image: "", multi: false, options: ["", ""], correct: 0, answers: [] };
}

function demoQuestions() {
  const picture = `${location.origin}/quiz/mark.svg`;
  return [
    {
      text: "Какой протокол держит живую связь в этом зале?",
      image: "",
      multi: false,
      options: ["HTTP", "WebSocket", "FTP", "SMTP"],
      correct: 1,
      answers: [],
    },
    {
      text: "Что написано на карточке?",
      image: picture,
      multi: false,
      options: ["Номер заказа", "Код комнаты", "Пароль почты", "Номер телефона"],
      correct: 1,
      answers: [],
    },
    {
      text: "Что нужно, чтобы ответить в этом квизе?",
      image: "",
      multi: true,
      options: ["Код комнаты", "Чужой пароль", "Вопрос на экране", "Почта ведущего"],
      correct: 0,
      answers: [0, 2],
    },
  ];
}

function readSaved(key) {
  try {
    return JSON.parse(localStorage.getItem(key) || "null");
  } catch {
    return null;
  }
}

const { createApp } = Vue;

createApp({
  data() {
    return {
      view: "gate",
      authMode: "login",
      form: { name: "", email: "", password: "", role: "organizer" },
      user: null,
      cabinet: { quizzes: [], hosted: [], played: [] },
      title: "Квиз на мероприятии",
      category: "Общее",
      customCategory: "",
      seconds: 20,
      rules: "Ответить можно, только пока вопрос на экране. Полный набор верных ответов даёт баллы, быстрый ответ даёт больше.",
      draft: [emptyQuestion()],
      joinCode: "",
      joinName: "",
      pendingJoin: false,
      previewing: false,
      you: null,
      yourAnswer: [],
      playerWs: null,
      error: "",
      status: "Не подключено",
      connected: false,
      ws: null,
      wsUrl: "",
      state: null,
      savedHost: null,
      picks: [],
      left: 0,
      copied: false,
      busy: false,
    };
  },
  computed: {
    phaseLabel() {
      const labels = { lobby: "Лобби", question: "Вопрос", reveal: "Ответ", done: "Итог" };
      return labels[this.state?.phase] || "";
    },
    isLast() {
      const question = this.state?.question;
      return !!question && question.index + 1 >= question.total;
    },
    joinUrl() {
      if (!this.state) return "";
      return `${location.origin}/quiz/#${this.state.code}`;
    },
    screenHref() {
      if (!this.state) return "/quiz/";
      return `/quiz/#watch/${this.state.code}`;
    },
    roleLabel() {
      return this.user?.role === "organizer" ? "организатор" : "участник";
    },
    canSubmitMulti() {
      const question = this.state?.question;
      const playing = this.previewing || this.state?.role === "player";
      const yours = this.previewing ? this.yourAnswer : question?.yourAnswer;
      return playing
        && this.state?.phase === "question"
        && question?.multi
        && !(yours && yours.length);
    },
    winnerLine() {
      const names = this.state?.winners || [];
      return names.length ? names.join(" и ") : "—";
    },
  },
  mounted() {
    const proto = location.protocol === "https:" ? "wss:" : "ws:";
    const path = location.pathname.replace(/\/$/, "");
    this.wsUrl = `${proto}//${location.host}${path}/ws`;
    this.savedHost = readSaved("quiz-host");
    this.user = readSaved("quiz-account");
    const hash = decodeURIComponent(location.hash.slice(1));
    if (hash.startsWith("watch/")) {
      this.connect({ op: "watch", code: hash.slice(6) });
      return;
    }
    if (/^[A-Za-z0-9]{4}$/.test(hash)) {
      this.joinCode = hash.toUpperCase();
      this.pendingJoin = true;
      if (!this.user?.token) this.view = "join";
    }
    if (this.user?.token) {
      this.refresh().then(() => {
        this.view = this.pendingJoin ? "join" : "home";
      }).catch(() => {
        this.logout(false);
      });
    }
    this._clock = setInterval(() => {
      if (this.state?.timed && this.state.phase === "question" && this.left > 0) this.left -= 1;
    }, 1000);
  },
  beforeUnmount() {
    clearInterval(this._clock);
  },
  methods: {
    letter(index) {
      return LETTERS[index] || String(index + 1);
    },
    spaced(code) {
      return String(code || "").split("").join(" ");
    },
    when(unix) {
      if (!unix) return "";
      return new Date(unix * 1000).toLocaleString("ru-RU", {
        day: "numeric", month: "short", hour: "2-digit", minute: "2-digit",
      });
    },
    apiBase() {
      return location.pathname.replace(/\/$/, "");
    },
    async api(path, body) {
      const headers = { "Content-Type": "application/json" };
      if (this.user?.token) headers.Authorization = `Bearer ${this.user.token}`;
      const response = await fetch(this.apiBase() + path, {
        method: body ? "POST" : "GET",
        headers,
        body: body ? JSON.stringify(body) : undefined,
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(data.message || "Не вышло");
      return data;
    },
    async refresh() {
      const data = await this.api("/api/cabinet");
      this.cabinet = {
        quizzes: data.quizzes || [],
        hosted: data.hosted || [],
        played: data.played || [],
      };
      this.user = { ...this.user, ...data.user };
      localStorage.setItem("quiz-account", JSON.stringify(this.user));
    },
    async submitAuth() {
      this.error = "";
      this.busy = true;
      try {
        const body = this.authMode === "register"
          ? this.form
          : { email: this.form.email, password: this.form.password };
        const data = await this.api(this.authMode === "register" ? "/api/register" : "/api/login", body);
        this.user = { ...data.user, token: data.token };
        localStorage.setItem("quiz-account", JSON.stringify(this.user));
        await this.refresh();
        this.view = this.pendingJoin ? "join" : "home";
      } catch (err) {
        this.error = err.message;
      } finally {
        this.busy = false;
      }
    },
    preview() {
      this.error = "";
      this.previewing = true;
      this.you = null;
      this.yourAnswer = [];
      this.user = { name: "Гость", role: "organizer", guest: true };
      const questions = demoQuestions().map((question) => ({
        text: question.text,
        image: question.image,
        multi: question.multi,
        options: question.options,
        answers: question.multi ? question.answers : [question.correct],
      }));
      this.connect({
        op: "create",
        preview: true,
        title: "Квиз на мероприятии",
        category: "IT",
        rules: "Ответить можно, только пока вопрос на экране. В этом просмотре кнопки ведущего и ответы на одном экране.",
        seconds: 30,
        questions,
      });
    },
    logout(toGate = true) {
      localStorage.removeItem("quiz-account");
      this.user = null;
      this.cabinet = { quizzes: [], hosted: [], played: [] };
      if (toGate) this.view = "gate";
    },
    freshQuiz() {
      this.title = "Квиз на мероприятии";
      this.category = "Общее";
      this.customCategory = "";
      this.seconds = 20;
      this.rules = "Ответить можно, только пока вопрос на экране. Полный набор верных ответов даёт баллы, быстрый ответ даёт больше.";
      this.draft = [emptyQuestion()];
      this.error = "";
      this.view = "build";
    },
    fillDemo() {
      this.draft = demoQuestions();
      this.title = "Квиз на мероприятии";
      this.category = "IT";
      this.seconds = 20;
    },
    addQuestion() {
      if (this.draft.length >= 20) return;
      this.draft.push(emptyQuestion());
    },
    addOption(question) {
      if (question.options.length >= 4) return;
      question.options.push("");
    },
    removeOption(question) {
      if (question.options.length <= 2) return;
      question.options.pop();
      if (question.correct >= question.options.length) question.correct = 0;
      question.answers = question.answers.filter((index) => index < question.options.length);
    },
    toggleAnswer(question, index) {
      const at = question.answers.indexOf(index);
      if (at >= 0) question.answers.splice(at, 1);
      else question.answers.push(index);
    },
    readImage(question, event) {
      const file = event.target.files && event.target.files[0];
      event.target.value = "";
      if (!file) return;
      if (file.size > 18000) {
        this.error = "Картинка больше 18 КБ. Оставьте ссылку на файл этого сайта";
        return;
      }
      const reader = new FileReader();
      reader.onload = () => {
        question.image = String(reader.result || "");
      };
      reader.readAsDataURL(file);
    },
    picture(value) {
      const raw = String(value || "").trim();
      if (!raw) return "";
      if (raw.startsWith("/")) return `${location.origin}${raw}`;
      return raw;
    },
    createRoom() {
      if (!this.user) {
        this.view = "gate";
        return;
      }
      const questions = [];
      for (const question of this.draft) {
        const options = question.options.map((item) => item.trim());
        if (question.text.trim().length < 3 || options.some((item) => !item)) {
          this.error = "Заполните вопрос и все варианты";
          return;
        }
        const answers = question.multi
          ? question.answers.slice().sort((a, b) => a - b)
          : [Number(question.correct)];
        if (!answers.length) {
          this.error = "Отметьте верный ответ";
          return;
        }
        const image = this.picture(question.image);
        if (image && !image.startsWith("data:image/") && !image.startsWith(`${location.origin}/`)) {
          this.error = "Картинка должна быть файлом этого сайта или небольшой загрузкой";
          return;
        }
        questions.push({
          text: question.text.trim(),
          image,
          multi: !!question.multi,
          options,
          answers,
        });
      }
      const category = this.category === "Своя" ? this.customCategory.trim() : this.category;
      this.connect({
        op: "create",
        token: this.user.token,
        title: this.title.trim(),
        category,
        rules: this.rules.trim(),
        seconds: Number(this.seconds),
        questions,
      });
    },
    openSaved(id) {
      this.connect({ op: "open", quizId: id, token: this.user.token });
    },
    joinRoom() {
      const payload = { op: "join", code: this.joinCode.trim() };
      if (this.user?.token) payload.token = this.user.token;
      else payload.name = this.joinName.trim();
      this.connect(payload);
    },
    resumeHost() {
      if (!this.savedHost) return;
      this.connect({ op: "host", code: this.savedHost.code, token: this.savedHost.token });
    },
    connect(payload) {
      this.error = "";
      if (this.ws) {
        this.ws.onclose = null;
        this.ws.close();
      }
      this.status = "Подключение...";
      let socket;
      try {
        socket = new WebSocket(this.wsUrl);
      } catch {
        this.status = "Ошибка подключения";
        this.error = "Не удалось открыть WebSocket";
        return;
      }
      this.ws = socket;
      socket.addEventListener("open", () => {
        this.connected = true;
        this.status = "Подключено";
        socket.send(JSON.stringify(payload));
      });
      socket.addEventListener("message", (event) => {
        try {
          this.onMessage(JSON.parse(event.data));
        } catch {
          this.error = "Сервер прислал не JSON";
        }
      });
      socket.addEventListener("error", () => {
        this.status = "Ошибка WebSocket";
      });
      socket.addEventListener("close", () => {
        if (this.ws !== socket) return;
        this.connected = false;
        this.status = "Отключено";
        this.ws = null;
      });
    },
    onMessage(data) {
      if (data.type === "error") {
        this.error = data.message;
        return;
      }
      if (data.type === "hello") {
        if (data.role === "host") {
          if (this.previewing) this.sitIn(data.code);
          else {
            const saved = { code: data.code, token: data.token };
            localStorage.setItem("quiz-host", JSON.stringify(saved));
            this.savedHost = saved;
          }
        }
        return;
      }
      if (data.type === "state") {
        const previous = this.state?.question?.index;
        this.state = data;
        this.view = "stage";
        this.error = "";
        if (data.phase === "question") {
          this.left = data.secondsLeft || 0;
          if (!data.question || data.question.index !== previous) {
            this.picks = [];
            this.yourAnswer = [];
          }
        }
        if (data.phase === "done" && this.user?.token) this.refresh().catch(() => {});
      }
    },
    send(payload) {
      if (!this.ws || this.ws.readyState !== 1) {
        this.error = "Нет соединения";
        return;
      }
      this.ws.send(JSON.stringify(payload));
    },
    sitIn(code) {
      if (this.playerWs) {
        this.playerWs.onclose = null;
        this.playerWs.close();
      }
      const socket = new WebSocket(this.wsUrl);
      this.playerWs = socket;
      socket.addEventListener("open", () => {
        socket.send(JSON.stringify({ op: "join", code, name: "Вы" }));
      });
      socket.addEventListener("message", (event) => {
        let data;
        try {
          data = JSON.parse(event.data);
        } catch {
          return;
        }
        if (data.type === "error") {
          this.error = data.message;
          return;
        }
        if (data.type === "state") {
          this.you = data.you || null;
          this.yourAnswer = data.question?.yourAnswer || [];
        }
      });
    },
    playerSend(payload) {
      if (!this.playerWs || this.playerWs.readyState !== 1) {
        this.error = "Нет соединения";
        return;
      }
      this.playerWs.send(JSON.stringify(payload));
    },
    choose(index) {
      const question = this.state?.question;
      if (!question || this.state.phase !== "question") return;
      if (this.previewing) {
        if (this.yourAnswer.length) return;
        if (question.multi) {
          const at = this.picks.indexOf(index);
          if (at >= 0) this.picks.splice(at, 1);
          else this.picks.push(index);
          return;
        }
        this.playerSend({ op: "answer", option: index });
        return;
      }
      if (this.state.role !== "player") return;
      if (question.yourAnswer && question.yourAnswer.length) return;
      if (question.multi) {
        const at = this.picks.indexOf(index);
        if (at >= 0) this.picks.splice(at, 1);
        else this.picks.push(index);
        return;
      }
      this.send({ op: "answer", option: index });
    },
    submitMulti() {
      if (!this.picks.length) {
        this.error = "Отметьте хотя бы один вариант";
        return;
      }
      const payload = { op: "answer", options: this.picks.slice() };
      if (this.previewing) this.playerSend(payload);
      else this.send(payload);
    },
    optionClass(index) {
      const question = this.state?.question;
      if (!question) return "";
      const yours = this.previewing ? (this.yourAnswer || []) : (question.yourAnswer || []);
      const correct = question.correct || [];
      const pending = yours.length ? yours : this.picks;
      if (!correct.length) return pending.includes(index) ? "is-picked" : "";
      if (correct.includes(index)) return "is-correct";
      if (yours.includes(index)) return "is-wrong";
      return "is-dim";
    },
    lockedChoice() {
      if (this.previewing) return this.state?.phase !== "question" || this.yourAnswer.length > 0;
      const yours = this.state?.question?.yourAnswer;
      return this.state?.role !== "player" || this.state?.phase !== "question" || (yours && yours.length);
    },
    barWidth(index) {
      const counts = this.state?.question?.counts || [];
      const total = counts.reduce((sum, value) => sum + value, 0) || 1;
      return `${Math.round((counts[index] / total) * 100)}%`;
    },
    async copyLink() {
      try {
        await navigator.clipboard.writeText(this.joinUrl);
        this.copied = true;
      } catch {
        this.copied = false;
        this.error = "Скопируйте ссылку под кнопками";
      }
    },
    leave() {
      if (this.ws) {
        this.ws.onclose = null;
        this.ws.close();
        this.ws = null;
      }
      if (this.playerWs) {
        this.playerWs.onclose = null;
        this.playerWs.close();
        this.playerWs = null;
      }
      this.connected = false;
      this.status = "Не подключено";
      this.state = null;
      this.you = null;
      this.previewing = false;
      if (this.user?.guest) this.user = null;
      this.view = this.user ? "home" : "gate";
      history.replaceState(null, "", location.pathname);
      if (this.user?.token) this.refresh().catch(() => {});
    },
  },
}).mount("#app");
