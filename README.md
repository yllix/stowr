<p align="center">
  <img src="branding/banner.svg" width="480" alt="stowr" />
</p>

<p align="center">
  <a href="https://github.com/yllix/stowr/actions/workflows/ci.yaml"><img src="https://github.com/yllix/stowr/actions/workflows/ci.yaml/badge.svg" alt="CI" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue" alt="License" /></a>
</p>

**Stowr** is a self-hosted web file manager. Point it at a directory and you get a clean web interface to browse, upload, download, preview, edit and share your files.

Stowr is a **maintained continuation of [File Browser](https://github.com/filebrowser/filebrowser)**, which was archived on 2026-09-01. It keeps everything File Browser users rely on and focuses on security fixes and careful improvements.

## Drop-in replacement for File Browser

Stowr is designed so that existing File Browser installations can switch without migrating data:

- The same database (`filebrowser.db`) and configuration file (`filebrowser.json`, `.filebrowser.*`) formats and default names.
- The same `FB_*` environment variables and command-line flags.
- The container image also ships a `/bin/filebrowser` symlink, so existing scripts and entrypoints keep working.

To switch a Docker or Kubernetes deployment, change only the image:

```sh
# Docker
docker run ... ghcr.io/yllix/stowr:latest

# Kubernetes (e.g. TrueNAS SCALE)
kubectl -n <namespace> set image deployment/<name> <container>=ghcr.io/yllix/stowr:<version>
```

Back up your database first: the first start upgrades it in place.

## Roadmap

1. **Security first.** Remove the command runner and hooks entirely, and replace stateless JWT sessions with server-side sessions that can be revoked (logout, password change, single-use refresh tokens).
2. **Audit the published advisories.** Verify that every File Browser security advisory is fixed in Stowr.
3. **Operations.** Built-in OIDC login, a Prometheus `/metrics` endpoint, structured and audit logging.
4. **Container hardening.** An image that runs cleanly as non-root with a read-only root filesystem.

## Installation

See [docs/installation.md](docs/installation.md). Container images are published to [`ghcr.io/yllix/stowr`](https://github.com/yllix/stowr/pkgs/container/stowr) for `amd64`, `arm64` and `armv7`.

## Security

Until the roadmap's security items land, the same precautions as File Browser apply:

- **Do not expose it directly to the internet.** Put it behind a reverse proxy that terminates TLS and performs its own authentication, or reach it over a VPN.
- **Keep command execution disabled** (the default).
- **Run it unprivileged**, inside a container, with only the directories you intend to serve mounted into it.

Report vulnerabilities privately; see [SECURITY.md](SECURITY.md).

## Documentation

Documentation on installing, configuring and building Stowr is in [docs](docs/). [CONTRIBUTING.md](CONTRIBUTING.md) covers building and development.

## Credits and license

Stowr is based on [File Browser](https://github.com/filebrowser/filebrowser), © File Browser Contributors, and keeps its full history. Stowr is not affiliated with or endorsed by the File Browser project. See [NOTICE](NOTICE) for details.

Licensed under the [Apache License 2.0](LICENSE).
