# Security policy

Steam Releaser is a local build and publishing tool. Its configurations, game
projects, engine executables, custom commands, and existing VDF scripts must come
from sources you trust. Building a project can run its editor scripts and build
tools with your user account's permissions. Inspect the configuration and
`steam-releaser plan` before building an unfamiliar project.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting on this repository's Security tab
when available. If private reporting is unavailable, open an issue asking for a
private reporting channel, without including exploit details or credentials.
Include the affected version, operating system, impact, and a minimal
reproduction once a private channel is established.

Fixes target the current development branch and latest release. Older releases
have no stated backport guarantee.

## Credentials and artifacts

SteamCMD owns its saved login tokens. Steam Releaser stores the account name
locally. The desktop's optional Remember Me setting keeps a password in memory
for the current app session; passwords and Steam Guard responses do not belong
in YAML, logs, environment files, or version control. Clearing the cached
username does not by itself prove that SteamCMD revoked its saved token.

The desktop also offers **Use an existing Steam credential file** in Client
settings. It reads the username and password from an existing JSON file without
creating or updating it, including during sign-in and clearing cached Steam
credentials. The file is unencrypted; keep it in a private location outside
version control with access limited to your account. The database stores only
the selected path and opt-in setting. File passwords are sent to SteamCMD through
its input pipe and are not returned to the frontend or passed as command arguments.
Steam Guard responses are never saved. Disabling the option leaves the file intact.

Use a Steam build account with access limited to the applications it needs to
publish. Review the resolved content directory and VDF mappings before uploading:
explicit content paths and hand-written VDF scripts control what SteamCMD sees.

Post-build copy paths are confined to their project/content roots, including
symlinks. Directory copies preserve relative links within the copied tree;
escaping or broken links are rejected. This confines artifact copying, while
custom commands and engine scripts still execute with your normal permissions.

Steam Releaser requires a matching SteamCMD completion line before reporting an
upload or preview as successful. For the default per-run output directory, it
can also inspect that run's build log. An unrecognized result, cancelled upload,
or output exceeding the capture limit can leave the upload status unconfirmed.
Check Steamworks before retrying, because an upload may have completed already.
Verify requested branch promotion there separately.
