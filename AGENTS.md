# Agent instructions

These instructions apply throughout this repository.

## Keep routine checks local

- Run tests, dependency checks, and release rehearsals locally. Use
  `bash scripts/check-local.sh` for the full local verification suite.
- GitHub-hosted workflows are reserved for building and publishing a release
  after a `v*` tag is pushed. Do not add push, pull-request, scheduled,
  manually dispatched, reusable, or one-off hosted test workflows.
- Do not trigger hosted Actions to validate changes; run the appropriate local
  checks instead. Native release builds are the only exception.

## Keep the user's desktop undisturbed

- Use headless, command-line, or background tools whenever they can accomplish the task. Prefer automated tests, builds, logs, and headless browser checks for validation.
- Do not open visible browser windows, desktop apps, game editors, file managers, or preview windows when a headless option is available. Avoid commands such as `open`, `xdg-open`, and development commands that automatically launch a GUI; disable automatic opening where supported.
- Run browser automation in headless mode. Run game engines and editors in their headless or batch modes when supported. Starting a GUI process in the background does not make it headless.
- Do not steal focus, move the pointer, type into desktop windows, or otherwise interfere with the user's keyboard, mouse, or display during routine work.
- If visible interaction is necessary and the user has not already requested or authorized it, explain why the available headless options are insufficient and ask before opening or controlling a GUI. Complete any independent headless work first.
