# Security Policy

## Supported versions

Only the latest Stowr release receives security fixes.

File Browser (the original project) is archived and receives no fixes. If you run File Browser, upgrading to Stowr is the supported path.

## Reporting a vulnerability

Please **do not open a public issue** for security problems.

Report privately through GitHub's [private vulnerability reporting](https://github.com/yllix/stowr/security/advisories/new). Include the affected version, steps to reproduce, and the impact you observed.

Before reporting, check the [existing advisories](https://github.com/yllix/stowr/security/advisories). Many issues in File Browser's [published advisories](https://github.com/filebrowser/filebrowser/security/advisories) also apply to the code Stowr inherited; reports confirming that one of those still affects Stowr are welcome.

## Known issue classes

These were inherited from File Browser and are being addressed first (see the README roadmap):

- **Command execution, runner and hooks.** Disabled by default. If enabled, treat it as equivalent to shell access on the host. Planned for removal.
- **Session and JWT handling.** Sessions are self-contained JWTs and cannot be revoked before they expire. Planned replacement with server-side sessions.
