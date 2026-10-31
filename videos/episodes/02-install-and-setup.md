---
number: 2
slug: install-and-setup
title: Install and set up a project
length: 75
audience: new users
rota: "0.28.0"
chain: new-project
---

# Episode 2: Install and set up a project

Written for a new user with a git repo and an agent (Claude Code or Codex). It installs the binary and the skills, seeds `.rota/`, checks the machine, then tunes the config with `/rota-init`. Shot grammar and fields: [`../README.md`](../README.md#script-format). Look and voice: [`../STYLE.md`](../STYLE.md).

### 01 motion

vo: Getting started takes three steps. Install Ro-Ta, set up your project, check that it works.

scene: Title card on `paper`. Three numbered rows enter one after another in Atkinson Hyperlegible Next: "1 install", "2 set up the project", "3 check". The number is `muted`, the label `ink`.

vertical: The three rows stack over the vertical centre, 88 px, each on its own line.

### 02 terminal

vo: Install the binary with Homebrew, or with the install script. It needs no sudo and puts Ro-Ta in your local bin folder.

```sh
curl -fsSL https://raw.githubusercontent.com/l4ci/rota/main/install.sh | sh
```

staged: install from release (network; the script downloads the release tarball and checks its sha256)

crop: keep the last 6 lines of the script output.

lower-third: `install.sh` | One binary, no sudo

vertical: Terminal box fills the frame width at 34 px; the long URL wraps onto two lines, output below.

### 03 terminal

vo: Check the version. Then install the skills. They go into the Claude Code and Codex skill folders.

```sh
rota version
rota skills install --agent claude
rota skills status
```

crop: rota skills install and rota skills status keep their last 3 lines each.

lower-third: `rota skills install` | Put the skills where your agent looks

vertical: Terminal box at 34 px; the crop keeps all three commands on screen with the newest line at the bottom.

### 04 terminal

vo: In your project, run init. It creates the dot rota folder, with your backlog, knowledge and decisions, and adds a few instruction files.

```sh
rota init
```

crop: keep the last 8 lines.

lower-third: `rota init` | Seed .rota/ in this repo

vertical: Terminal box fills the frame width at 34 px; the 8 kept lines fit without scrolling.

### 05 terminal

vo: Doctor checks git, your tools and the installed skills. Each failure comes with a hint.

```sh
rota doctor
```

crop: keep the first 7 lines.

lower-third: `rota doctor` | Preflight for the machine and the repo

vertical: Terminal box at 34 px; the 7 kept lines fit without scrolling and `pass` lines show in `merged`.

### 06 terminal

vo: Init takes defaults. To tune them, open your agent and run the init skill. It suggests values from your repo, and you confirm each one.

```sh
/rota-init
```

staged: claude session with rota skills installed, in a Go project; the skill asks three config questions and shows its evidence for each

crop: keep the first question and its suggestion, about 10 lines.

lower-third: `/rota-init` | Suggests config from your repo, you confirm

vertical: Terminal box at 34 px; the question and the suggestion stay on screen, answer typed on the last line.

### 07 motion

vo: Coding agents write fast. Ro-Ta makes sure what they merge works. Find it at github dot com slash l four c i slash Ro-Ta.

scene: The outro card from STYLE.md, unchanged: icon, two lines, link band `github.com/l4ci/rota`.

vertical: Outro card per STYLE.md, link set on two lines at 64 px.
