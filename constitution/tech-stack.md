# Tech stack

## Rules for every project

The same in every project.

### Constitution

- **G-CON-1** The constitution is the source of truth.
- **G-CON-2** The project's `constitution/` holds three files: `mission.md`, `tech-stack.md` and `roadmap.md`.
- **G-CON-3** Read it at the start of every session. Follow it without exceptions.
- **G-CON-4** `mission.md` and `tech-stack.md` hold lasting rules. `roadmap.md` holds every plan: the MVPs in order, what each holds, what comes later, and open questions.
- **G-CON-5** Coding and writing guidelines live in `tech-stack.md`.
- **G-CON-6** A new rule is written straight into the file it belongs to.

### Stack

- **G-STACK-1** The language is used at its latest stable release.
- **G-STACK-2** Packages are managed with the language's own package manager. Tests run with its standard test runner.
- **G-STACK-3** Lint runs with the language's standard linter, default settings.
- **G-STACK-4** API first: every feature is in the API. The CLI and the web UI are clients of it, and each reaches every feature. No feature exists twice.
- **G-STACK-5** CLI commands put the verb first, like kubectl: `<app> <verb> <kind> <name>`. Every command can print `-o table` (the default), `-o json` or `-o yaml`.
- **G-STACK-6** `delete` asks the user to type the name first; `--yes` skips that for scripts.
- **G-STACK-7** A web UI shows state and makes the tool easier to use. It holds no logic.
- **G-STACK-8** Config lives in `~/.config/<app>`. Data lives in `~/.local/share/<app>`.
- **G-STACK-9** Built-in files are never edited. A custom file overrides the built-in file at the same relative path. An upgrade replaces built-in files and leaves custom files alone.

### Planning

- **G-PLAN-1** Plan first, get approval, then code. Never code without clear approval.
- **G-PLAN-2** The plan for a card is written on the card.
- **G-PLAN-3** Approval is an explicit yes from the project owner, or the `#approved` tag on the card.
- **G-PLAN-4** Split the features into MVPs. Work only on the current MVP. No drifting.
- **G-PLAN-5** When an MVP starts, its cards are made on the board from `roadmap.md`. The board holds only the current MVP's cards.
- **G-PLAN-6** Finish the current MVP's cards before the next MVP starts.
- **G-PLAN-7** Work in small steps: one card per step.
- **G-PLAN-8** Something found while working a card becomes a new card. It never widens the current card.
- **G-PLAN-9** The e-designer designs are drawn only when the project owner explicitly asks.

### Cards and branches

- **G-CARD-1** Each feature gets a kanban card, made with the `kanban` CLI.
- **G-CARD-2** Each card names the feature it belongs to.
- **G-CARD-3** Cards outside the current MVP are not worked on.
- **G-CARD-4** Archived cards are ignored.
- **G-CARD-5** Each card gets its own branch, and the card records the branch name in its `branch` field.
- **G-CARD-6** Branch names are `<type>/<slug>`, using the Conventional Commits type, like `feat/add-search`.
- **G-CARD-7** The project owner moves cards from Backlog to Refined.
- **G-CARD-8** Whoever works a card moves it to Review when the work is done.
- **G-CARD-9** The project owner reviews the cards in Review, and only the owner moves a card to Done.
- **G-CARD-10** No code changes without a card and a branch. A bug found during work gets its own card, branch, fix and merge.

### Code

- **G-CODE-1** Follow SOLID and every grade of the [Clean Code Developer](https://clean-code-developer.com) rules. The grades are levels of practices, from red to white, each adding to the last.
- **G-CODE-2** Follow the deep modules principle from John Ousterhout's *A Philosophy of Software Design*: a module hides a lot of work behind a small, simple interface. Prefer few deep modules over many shallow ones.
- **G-CODE-3** Code should explain itself and be easy to read.
- **G-CODE-4** Comments are one line. No history and no needless explanation in comments.
- **G-CODE-5** A file is written whole or not at all: into a temporary file beside it, then renamed into place.

### Tests

- **G-TEST-1** Unit tests support SOLID and the other design principles.
- **G-TEST-2** Tests check behaviour through public interfaces, not internals.
- **G-TEST-3** A new feature adds tests and does not change existing ones, unless it changes that behaviour on purpose.
- **G-TEST-4** No unnecessary tests and no over-engineering. Build and test what the current card needs, nothing more.

### Commits and pull requests

- **G-GIT-1** Commits follow Conventional Commits, as a one-line subject. No co-author lines.
- **G-GIT-2** Pull requests are short and easy to read.
- **G-GIT-3** All code is approved by the project owner before it reaches `main`.
- **G-GIT-4** For now, the owner reviews the branch locally before it is merged.
- **G-GIT-5** A merge makes a single commit on `main` (squash merge).

### Writing

- **G-WRITE-1** Docs and text use simple, short, clear words. No jargon.
- **G-WRITE-2** A project's own words are listed in a Words table in `mission.md`, and each means one thing.
