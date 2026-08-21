# Исследование скилов Claude Code

## Резюме

Проверил официальные источники (anthropics/skills, anthropics/claude-plugins-official) и крупные community-коллекции по GitHub API — метаданные (звёзды, дата последнего пуша, лицензия) и содержимое SKILL.md смотрел напрямую, не по описаниям. Под стек Gul прицельно подходят: samber/cc-skills-golang (Go performance/concurrency/testing/benchmark/CI), obra/superpowers (TDD, systematic debugging, планы, worktrees), mattpocock/skills (инженерная дисциплина по TypeScript) и первопартийные Anthropic-плагины gopls-lsp, clangd-lsp, typescript-lsp, pr-review-toolkit, commit-commands и security-guidance. Специализированных качественных скилов под Wails v3, cgo-аудио/DSP, golden-тесты и zustand/Vite нет — это придётся закрывать проектными правилами. Много мусора отсеяно: десятки репозиториев с 0–5 звёздами и AI-сгенерированными SKILL.md, а также «awesome»-каталоги с низким сигналом.

## Находки

### [MAJOR] samber/cc-skills-golang — лучший набор Go-скилов под наш профиль

46 скилов (проверено через API: skills/ содержит ровно 46 директорий). Напрямую релевантны: golang-performance (аллокации, memory layout, GC, hot-path, требует benchstat), golang-concurrency (утечки горутин, ownership каналов, structured concurrency), golang-testing (table-driven, goleak, fuzzing, snapshot, t.Parallel), golang-benchmark (методология измерений, pprof, trace), golang-safety, golang-lint, golang-error-handling, golang-project-layout, golang-continuous-integration (GitHub Actions для Go: тесты, линт, SAST, coverage, Dependabot/Renovate, GoReleaser), golang-troubleshooting (есть references/compilation.md с cgo). Качество высокое: 3021 звезда, последний пуш 2026-08-20, MIT, автор — samber (автор samber/lo). SKILL.md написаны структурно: frontmatter с description/allowed-tools/paths, режимы работы (Write/Review/Audit/Debug), явные ссылки на смежные скилы, references/ и evals/. В репозитории есть EVALUATIONS.md с замерами uplift «со скилом vs без» по каждому скилу — редкий признак реальной проверки, а не AI-слопа. Оговорка: 7 скилов (golang-samber-lo/do/mo/oops/slog/ro/hot) продвигают библиотеки автора — их ставить не нужно, они нам не по стеку. cgo упоминается всего в 18 файлах и глубокой проработки cgo нет.

Источники: <https://github.com/samber/cc-skills-golang> · <https://github.com/samber/cc-skills-golang/blob/main/EVALUATIONS.md> · <https://raw.githubusercontent.com/samber/cc-skills-golang/main/skills/golang-performance/SKILL.md> · <https://raw.githubusercontent.com/samber/cc-skills-golang/main/skills/golang-concurrency/SKILL.md>

### [MAJOR] anthropics/claude-plugins-official — официальный маркетплейс

33 769 звёзд, пуш 2026-08-21. В marketplace.json 286 плагинов. Первопартийные (папка plugins/, автор — Anthropic) и релевантные нам: gopls-lsp, typescript-lsp, clangd-lsp, pr-review-toolkit, code-review, code-simplifier, security-guidance, claude-security, commit-commands, feature-dev, frontend-design, skill-creator, claude-md-management и claude-code-setup. Плюс сторонние из официального списка: superpowers (obra), mattpocock-skills, playwright, chrome-devtools-mcp, context7, semgrep и codspeed.

Источники: <https://github.com/anthropics/claude-plugins-official> · <https://github.com/anthropics/claude-plugins-official/blob/main/.claude-plugin/marketplace.json>

### [MAJOR] gopls-lsp (Anthropic) — LSP-интеллект для Go-ядра

Первопартийный плагин из claude-plugins-official (путь plugins/gopls-lsp). Даёт агенту настоящую навигацию по Go-коду: переходы к определениям, поиск ссылок, рефакторинги — вместо grep по тексту. Для проекта, где жёсткое правило «сигнатуры внешних API сверять с godoc/исходниками, не по памяти», это самый прямой способ снизить риск выдуманных API. Требует go install golang.org/x/tools/gopls@latest и $GOPATH/bin в PATH. Качество: код лежит в официальном репо Anthropic, README короткий но по делу; риска «мусора» нет.

Источники: <https://github.com/anthropics/claude-plugins-official/tree/main/plugins/gopls-lsp>

### [MAJOR] clangd-lsp (Anthropic) — LSP для C/C++ части, то есть для cgo и DSP

Первопартийный плагин из того же маркетплейса (plugins/clangd-lsp), заявлен как «C/C++ language server (clangd) for code intelligence». У вас cgo + аудио-DSP (AEC/AGC/RNNoise) — то есть реальный C-код и C-заголовки в дереве. Без clangd агент читает .h/.c как текст и легко путает сигнатуры и типы. Это единственный найденный инструмент, который вообще закрывает C-сторону cgo; специализированных «cgo-скилов» в природе нет.

Источники: <https://github.com/anthropics/claude-plugins-official/blob/main/.claude-plugin/marketplace.json>

### [MAJOR] obra/superpowers — TDD, systematic debugging и дисциплина процесса

275 333 звезды, пуш 2026-08-19, MIT, версия плагина 6.3.0, автор Jesse Vincent. 14 скилов: test-driven-development, systematic-debugging, requesting-code-review, receiving-code-review, verification-before-completion, writing-plans, executing-plans, brainstorming, subagent-driven-development, using-git-worktrees, finishing-a-development-branch, dispatching-parallel-agents, writing-skills. Содержимое смотрел напрямую: TDD-скил формулирует «Iron Law» (никакого продакшн-кода без падающего теста, написал раньше — удали и перепиши), systematic-debugging требует найти root cause до любого фикса и делит работу на 4 фазы. Это ровно та дисциплина, которую вы прописали в правилах проекта. Есть в официальном маркетплейсе Anthropic, так что ставится без добавления стороннего marketplace. Оговорка: набор довольно «навязчивый» — он перехватывает начало работы (brainstorm → plan → execute) и может конфликтовать с вашим правилом «работать в рамках текущего милстоуна PLAN.md §7»; при установке стоит сразу посмотреть, не начнёт ли он тянуть в свой собственный процесс планирования.

Источники: <https://github.com/obra/superpowers> · <https://raw.githubusercontent.com/obra/superpowers/main/skills/test-driven-development/SKILL.md> · <https://raw.githubusercontent.com/obra/superpowers/main/skills/systematic-debugging/SKILL.md> · <https://github.com/obra/superpowers-marketplace>

### [MAJOR] mattpocock/skills — инженерные скилы под TypeScript/фронтенд

228 098 звёзд, пуш 2026-08-21, MIT, автор Matt Pocock (Total TypeScript). Есть в официальном маркетплейсе как mattpocock-skills. Раздел skills/engineering: tdd, code-review, codebase-design, domain-modeling, diagnosing-bugs, improve-codebase-architecture, resolving-merge-conflicts, implement, to-spec, to-tickets, triage, research. Проверял содержимое: скил tdd вводит понятие «seam» (публичная граница, на которой пишется тест), требует согласовать seams до написания тестов, разбирает анти-паттерны (implementation-coupled, тавтологические тесты, horizontal slicing) и настаивает на вертикальных срезах — это качественная, неочевидная методика, а не пересказ википедии. Скил code-review делает двухосевой ревью (Standards + Spec) параллельными сабагентами. Прямого React 19/zustand/Vite контента нет — это про инженерную дисциплину, а не про API фреймворков. Оговорка: скилы code-review и to-spec/to-tickets ожидают файл docs/agents/issue-tracker.md и запуск /setup-matt-pocock-skills, иначе ругаются.

Источники: <https://github.com/mattpocock/skills> · <https://github.com/mattpocock/skills/blob/main/skills/engineering/tdd/SKILL.md> · <https://github.com/mattpocock/skills/blob/main/skills/engineering/code-review/SKILL.md>

### [MAJOR] samber/cc-skills → скил conventional-git — под ваш git-процесс

Репозиторий: 188 звёзд, пуш 2026-08-19, MIT, тот же автор. Нужен ровно один скил из него — conventional-git (SKILL.md 7454 байта + папка evals). Покрывает Conventional Commits v1.0.0: формат сообщений, именование веток вида <type>/[issue-]<description>, именование worktree под .claude/worktrees/, привязка к SemVer и автогенерации changelog, авто-закрытие issue. Это точно ложится на ваше правило «коммиты — conventional». Остальные скилы репозитория (копирайтинг, LinkedIn, пресс-релизы, chrome-extension) нам не нужны — ставить весь плагин целиком не стоит.

Источники: <https://github.com/samber/cc-skills> · <https://raw.githubusercontent.com/samber/cc-skills/main/skills/conventional-git/SKILL.md>

### [MAJOR] Пробел: под Wails v3, cgo-аудио/DSP, golden-тесты и zustand/Vite качественных скилов не существует

Целенаправленно искал по GitHub: запросы про wails, audio dsp, golden file / snapshot testing, zustand, react 19 + vite дают либо шум (случайные репозитории), либо проекты с 0-5 звёздами и явно AI-сгенерированными SKILL.md. Ни одного кандидата, который прошёл бы порог качества. Вывод: самые проектно-специфичные правила из вашего CLAUDE.md — сетка 48k/10ms/480, один full-duplex девайс, порядок AEC→AGC→RNNoise→gate, ноль аллокаций и блокировок в аудио-колбэке, тонкие services/ и логика в internal/, UI не трогает звук и сеть, golden-тесты DSP — никакой внешний скил не закроет. Их надо оформить своим скилом в .claude/skills/ проекта, иначе они останутся только в CLAUDE.md и будут вымываться из контекста на длинных сессиях.

Источники: <https://github.com/samber/cc-skills-golang>

### [MAJOR] Локальная конфигурация должна оставаться приватной

Перед установкой нужно проверить дубли встроенных и сторонних скилов, доступность LSP-бинарников и состояние MCP-авторизации. Пользовательские настройки, список подключённых сервисов и проектные рабочие копии не должны попадать в публичный репозиторий; каталог `.claude/` поэтому хранится только локально и исключён через `.gitignore`.

Источники: <https://github.com/anthropics/claude-plugins-official>

### [minor] pr-review-toolkit и commit-commands (Anthropic) — code review и git-рутина

Оба первопартийные, из claude-plugins-official. pr-review-toolkit содержит 6 агентов: code-reviewer, code-simplifier, comment-analyzer, pr-test-analyzer, silent-failure-hunter, type-design-analyzer. silent-failure-hunter (7807 байт) особенно уместен для аудио-пайплайна, где проглоченная ошибка = тишина в эфире, а не краш. commit-commands даёт команды commit, commit-push-pr, clean_gone. Оговорка: в вашей сборке Claude Code уже есть встроенные скилы code-review и simplify (видны в списке доступных скилов сессии), так что pr-review-toolkit частично дублирует их — берите ради специализированных агентов, а не ради базового ревью.

Источники: <https://github.com/anthropics/claude-plugins-official/tree/main/plugins/pr-review-toolkit> · <https://github.com/anthropics/claude-plugins-official/tree/main/plugins/commit-commands>

### [minor] awesome-skills/code-review-skill — широкий ревью-справочник, но тело на китайском

1772 звезды, пуш 2026-07-16, MIT. Прогрессивное раскрытие: ядро SKILL.md ~11.7 КБ, а справочники грузятся по надобности — reference/go.md (20 КБ), typescript.md (24 КБ), react.md (22 КБ), css-less-sass.md (13 КБ), security-review-guide.md (13 КБ), performance-review-guide.md (19 КБ), common-bugs-checklist.md, architecture-review-guide.md. Формально закрывает сразу Go + TS + React 19 + CSS, чего нет больше нигде в одном месте. Существенная оговорка по качеству: проверил reference/react.md и reference/go.md — проза внутри написана на китайском (код и идентификаторы английские). Модели это не мешает, но вы не сможете быстро вычитать и адаптировать правила под свои жёсткие требования (аудио-сетка, ноль аллокаций), а именно это в проекте важнее общего чек-листа. Плюс частично дублирует встроенный /code-review.

Источники: <https://github.com/awesome-skills/code-review-skill> · <https://github.com/awesome-skills/code-review-skill/blob/main/reference/react.md> · <https://github.com/awesome-skills/code-review-skill/blob/main/reference/go.md>

### [minor] Security review: google/mantis и первопартийные варианты Anthropic

google/mantis — 763 звезды, пуш 2026-08-17, Apache-2.0, 18 скилов-стадий (threat-model, plan, researcher, reproduce, patch, critic, dedupe, calibrate, report...). Это полноценный конвейер автономного поиска уязвимостей, а не «скил на почитать»; README сам предупреждает капсом, что он генерирует и выполняет автономно сгенерированный код и его нельзя запускать на машине с доступом к проду/секретам/внутренней сети. Для десктопного голосового клиента это перебор — годится максимум для разовой изолированной прогонки. Более уместные варианты: security-guidance и claude-security из claude-plugins-official (первопартийные, security-guidance вешает хуки на редактирование и на Stop), anthropics/defending-code-reference-harness (7336 звёзд, пуш 2026-08-06 — скилы threat modeling / scanning / triage / patching), anthropics/claude-code-security-review (5983 звезды, MIT — GitHub Action для ревью диффа в CI, ложится в ваш GitHub Actions pipeline). И напоминание: в вашей сборке уже есть встроенный /security-review.

Источники: <https://github.com/google/mantis> · <https://github.com/anthropics/defending-code-reference-harness> · <https://github.com/anthropics/claude-code-security-review> · <https://github.com/anthropics/claude-plugins-official/tree/main/plugins/security-guidance>

### [minor] Tailwind: Lombiq/Tailwind-Agent-Skills — единственный вменяемый, но с оговорками

65 звёзд, пуш 2026-04-09, BSD-3-Clause, ветка по умолчанию dev. Один скил tailwind-4-docs: SKILL.md (4.7 КБ) + references/engineering-playbook.md (9.3 КБ) + references/gotchas.md (1.7 КБ) + scripts/sync_tailwind_docs.py. Идея правильная — локальный снапшот официальных доков Tailwind v4 с индексом, чтобы не галлюцинировать утилиты и не путать v3/v4. Оговорки: (1) сама документация НЕ входит в репозиторий, её надо синхронизировать скриптом с tailwindlabs/tailwindcss.com, который source-available, но не open-source — лицензию принимаете вы; (2) скил требует пересинхронизации раз в неделю и отказывается отвечать без снапшота; (3) пуш четырёхмесячной давности. Альтернатива без этих сложностей — уже подключённый у вас MCP context7 (query-docs/resolve-library-id), который тянет актуальные доки Tailwind/React/Vite/zustand по запросу.

Источники: <https://github.com/Lombiq/Tailwind-Agent-Skills> · <https://github.com/Lombiq/Tailwind-Agent-Skills/blob/dev/skills/tailwind-4-docs/SKILL.md>

### [minor] GitHub Actions: retlehs/gh-actions и Seldaek/zizmorify — маленькие, но не мусорные

retlehs/gh-actions (11 звёзд, пуш 2026-04-13, автор — Ben Word из Roots): один SKILL.md на 4221 байт. Содержательный: запрещает угадывать версии экшенов и требует проверять их через gh api repos/{owner}/{action}/releases/latest; требует top-level permissions по принципу наименьших привилегий (начинать с permissions: {}), запрещает write-all и pull_request_target без нужды, запрещает интерполировать ${{ github.event.* }} прямо в run: (expression injection). Seldaek/zizmorify (11 звёзд, пуш 2026-05-29, автор — Jordi Boggiano, автор Composer): добавляет статический анализатор workflow-файлов zizmor в CI и чинит найденные проблемы. Оба репозитория «маленькие по звёздам, но авторы известные и содержимое конкретное» — это не AI-слоп. Для Go-специфичного CI (matrix, кэш модулей, race-детектор, coverage, GoReleaser) лучше подходит golang-continuous-integration из samber/cc-skills-golang.

Источники: <https://github.com/retlehs/gh-actions> · <https://raw.githubusercontent.com/retlehs/gh-actions/main/SKILL.md> · <https://github.com/Seldaek/zizmorify>

### [info] anthropics/skills — официальный репозиторий скилов: под наш стек в нём почти ничего

170 778 звёзд, пуш 2026-08-18, маркетплейс называется anthropic-agent-skills, плагины: document-skills, example-skills, claude-api, academy-guide, discernment-nudge. Всего 19 скилов. Под наш стек подходят только два: frontend-design (сильный текст про дизайн-направление, типографику, борьбу с «AI-шаблонностью» — полезен, но у вас уже есть Gul-Prototype-offline.html как источник визуальной правды, так что он может конфликтовать с прототипом) и webapp-testing (Playwright-скрипты + scripts/with_server.py для подъёма dev-сервера — применим к React-части, которую можно гонять через vite dev отдельно от Wails-вебвью). Go, Wails, аудио, Tailwind, zustand — не покрыты вообще. Плюс: document-skills (docx/pdf/xlsx/pptx), claude-api и skill-creator у вас уже включены в сессии как anthropic-skills:* — второй раз ставить не надо.

Источники: <https://github.com/anthropics/skills> · <https://github.com/anthropics/skills/blob/main/.claude-plugin/marketplace.json> · <https://raw.githubusercontent.com/anthropics/skills/main/skills/webapp-testing/SKILL.md> · <https://raw.githubusercontent.com/anthropics/skills/main/skills/frontend-design/SKILL.md>

### [info] Каталоги и маркетплейсы: чем пользоваться и чему не верить

Полезные для дальнейшего поиска: hesreallyhim/awesome-claude-code (52 754 звезды, пуш 2026-08-21) — курируемый вручную и обновляемый; VoltAgent/awesome-agent-skills (30 655 звёзд, 1000+ скилов, пуш 2026-08-21) — большой охват, но именно каталог, качество отдельных записей не гарантировано; skills.sh (Agent Skills Directory, поддерживается Vercel) — универсальный установщик npx skills add и лидерборд по установкам, полезен как метрика реального использования, а не только звёзд. Не рекомендую опираться на: ComposioHQ/awesome-claude-skills (72 940 звёзд при том, что это просто список — соотношение звёзд к содержанию говорит о накрутке/маркетинге), travisvn/awesome-claude-skills (14 750 звёзд, но последний пуш 2026-04-28 — устарел на 4 месяца), а также на десятки репозиториев вида «N skills for Claude Code» с сотнями скилов и 0-50 звёздами — выборочная проверка показывает шаблонные SKILL.md без references, без evals и без признаков ручной правки.

Источники: <https://github.com/hesreallyhim/awesome-claude-code> · <https://github.com/VoltAgent/awesome-agent-skills> · <https://skills.sh/> · <https://github.com/ComposioHQ/awesome-claude-skills>

## Рекомендации

- Ставить в первую очередь (плагины, глобально, маркетплейс уже подключён): /plugin install gopls-lsp@claude-plugins-official, /plugin install clangd-lsp@claude-plugins-official (нужен для cgo/C-DSP), /plugin install typescript-lsp@claude-plugins-official. Для gopls предварительно: go install golang.org/x/tools/gopls@latest и $GOPATH/bin в PATH.
- Go-скилы — выборочно, не весь плагин: npx skills add https://github.com/samber/cc-skills-golang --skill golang-performance --skill golang-concurrency --skill golang-testing --skill golang-benchmark --skill golang-safety --skill golang-lint --skill golang-error-handling --skill golang-project-layout --skill golang-continuous-integration --skill golang-troubleshooting. Ставить в проект (../../.claude/skills/), а не глобально — они Go-специфичны. Скилы golang-samber-* (lo/do/mo/oops/slog/ro/hot) НЕ брать: продвигают библиотеки автора, у нас другой стек.
- Процесс и TDD: /plugin install superpowers@claude-plugins-official и /plugin install mattpocock-skills@claude-plugins-official — оба глобально. После установки superpowers прогнать одну реальную задачу и проверить, не перехватывает ли его brainstorm/write-plan ваш процесс по PLAN.md §7; если конфликтует — оставить только скилы test-driven-development и systematic-debugging, скопировав их в ~/.claude/skills/, и не ставить плагин целиком.
- Git-процесс: npx skills add https://github.com/samber/cc-skills --skill conventional-git в ~/.claude/skills/ (это правило глобальное, не проектное). Дополнительно /plugin install commit-commands@claude-plugins-official, если нужны готовые команды commit / commit-push-pr.
- Написать свой проектный скил — это главное, чего не закрывает ни один внешний: ../../.claude/skills/gul-audio-core/SKILL.md с жёсткими правилами (48k/10ms/480, один full-duplex девайс, порядок AEC→AGC→RNNoise→gate, ноль аллокаций и блокировок в аудио-колбэке, тонкие services/, логика в internal/, UI не трогает звук и сеть, golden-тесты DSP: формат фикстур, допуск, как перегенерировать). Использовать для этого уже включённый anthropic-skills:skill-creator. Второй проектный скил — gul-wails-ui: границы Wails v3 bindings, React 19 + zustand + Vite конвенции, соответствие Gul-Prototype-offline.html.
- CI и безопасность — по мере надобности, не сразу: golang-continuous-integration (уже в списке выше) закрывает Go-часть GitHub Actions; npx skills add https://github.com/retlehs/gh-actions в проект — если нужны общие правила по permissions/версиям/expression injection; anthropics/claude-code-security-review — как GitHub Action в CI. Встроенных /code-review и /security-review для повседневной работы достаточно — google/mantis не ставить (запускать только изолированно и разово, README прямо запрещает прогон на машине с доступом к проду).
- Не ставить: awesome-skills/code-review-skill (тело справочников на китайском — не сможете вычитать и адаптировать под свои жёсткие правила, плюс дублирует встроенный /code-review); Lombiq/Tailwind-Agent-Skills (требует ручной синхронизации доков под несвободной лицензией и еженедельного обновления) — вместо него использовать уже подключённый MCP context7 для актуальных доков Tailwind v4 / React 19 / Vite / zustand.
- Гигиена перед установкой: проверять доступность упомянутых агентов и LSP-инструментов; отдельно настраивать или отключать MCP-сервисы, требующие авторизации; после каждой установки сверять список скилов, чтобы не набрать дублей встроенных code-review / security-review / simplify. Локальную конфигурацию не коммитить.
