# Security Policy

## Supported Versions

Only the latest released version is supported. Security fixes are not backported to older tags.

| Version  | Supported          |
| -------- | ------------------ |
| latest   | :white_check_mark: |
| < latest | :x:                |

## Reporting a Vulnerability

Please follow these steps if you discover a security vulnerability in this project:

### Do Not

- **Do not** open a public GitHub issue for security vulnerabilities
- **Do not** disclose the vulnerability publicly until it has been addressed

### Do

1. **Report privately** via [GitHub Security Advisories](https://github.com/miikkak/mc-run/security/advisories/new) <!-- markdownlint-disable-line MD013 -->
2. **Include in your report:**
   - Description of the vulnerability
   - Steps to reproduce the issue
   - Potential impact
   - Suggested fix (if you have one)

3. **Response timeline:**
   - You should receive an acknowledgment within 48 hours
   - We'll provide a detailed response within 7 days
   - We'll work with you to understand and fix the issue
   - We'll release a fix as soon as possible

## Scope

This runs as PID 1 in a container, supervising a single child process it was told to launch on
the command line - it doesn't accept network connections or untrusted input of its own. Its
actual attack surface is narrow: the optional named-pipe console (local filesystem access only,
matching the container's own trust boundary), the `RCON_PASSWORD`/`RCON_CONFIG_FILE` environment
variables it reads to attempt a graceful RCON stop, and the `update-plugins`/`install-plugins`
subcommands, which open and inspect jars as zip archives (never execute their contents) to match
plugin descriptors. If you find a way this tool could be used to escalate privileges, execute
unintended code, or misbehave beyond its documented process-supervision behavior, that's exactly
the kind of thing to report.

## Security Best Practices

When using this tool:

- Always use a specific released version, not a locally built development binary, in production
- Treat `RCON_PASSWORD` like any other credential - prefer `RCON_CONFIG_FILE` with restricted
  file permissions over a plaintext environment variable where that's practical for your setup
- The named pipe and `plugins/update/`/`plugins/install/` directories are only as trustworthy as
  whatever has write access to them - this tool assumes the container's own filesystem is
  already within your trust boundary, the same assumption the container itself makes
- Keep the tool updated - check releases periodically or watch the repository

## Security Scanning

This project uses automated security scanning:

- **Trivy** (filesystem scan against `go.sum`) for dependency vulnerability scanning, on a
  weekly schedule and on demand
- **golangci-lint** (including security-relevant linters) on every PR
- **Renovate** for automated dependency updates

Note this project is stdlib-only by design (see `go.mod`), which keeps the dependency surface
Trivy has to scan minimal by construction, not just by policy.

## Other Automated Review

Every pull request also gets an AI code review. This is a general correctness/quality review,
not a vulnerability scanner - don't rely on it as a substitute for the security scanning above.

## Disclosure Policy

- Security issues are fixed in private before public disclosure
- After a fix is released, we publish a security advisory
- We credit reporters in the advisory (unless they prefer anonymity)

## Past Security Advisories

No security advisories have been published yet.

## Contact

For security-related questions or concerns, please use the reporting method above rather than
public channels.
