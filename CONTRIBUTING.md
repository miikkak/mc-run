# Contributing to mc-run

Thank you for considering contributing to this project! We welcome contributions from the
community.

## Getting Started

1. Fork the repository
2. Clone your fork locally
3. Create a new branch for your changes
4. Make your changes
5. Test your changes
6. Submit a pull request

## Forking this Repository

If you're creating your own fork of this project, you'll need to update repository-specific
references:

### Files to Update

1. **`renovate.json`**
   - Update the `extends` entries pointing at `github>miikkak/renovate-config` to your own
     Renovate config, or drop them if you don't use Renovate

2. **`SECURITY.md`**
   - Update the GitHub Security Advisories URL to point to your fork

3. **`README.md`**
   - Update any references to `miikkak/mc-run` or `miikkak/mc-server-container` to point to
     your fork, if relevant to your use case

This list isn't exhaustive - grep for `miikkak` across the repo to catch anything else hardcoded
to the canonical repository, then update it to point to your fork's location.

## Development Requirements

- Go (see `go.mod` for the minimum version)
- Git
- Pre-commit hooks
- [`golangci-lint`](https://golangci-lint.run/) (used by the pre-commit hook and CI)

## Code Quality Standards

This project maintains high code quality standards using automated tooling:

### Pre-commit Hooks

All commits must pass pre-commit hooks. Install the `pre-commit` tool itself first (see
[pre-commit.com's installation guide](https://pre-commit.com/#installation) for other methods,
e.g. your OS package manager):

```bash
pip install pre-commit
```

Then register the hooks for this repo:

```bash
pre-commit install
```

The hooks cover `gofmt`, `golangci-lint`, `go vet`, and basic file hygiene (trailing whitespace,
line endings, valid YAML/JSON, no accidentally-committed large files).

### Commit Message Format

We use [Conventional Commits](https://www.conventionalcommits.org/) — this isn't enforced by a
bot, but `semantic-release` (triggered by the PR's release label, not commit message parsing)
relies on the convention for consistency. Your commit messages should follow this format:

```text
<type>(<scope>): <description>

[optional body]

[optional footer]
```

Types: `feat`, `fix`, `docs`, `chore`, `refactor`, `test`, `ci`

Examples:

- `feat: add install-plugins subcommand`
- `fix: correct SIGTERM handling when RCON stop fails`
- `docs: document the update-plugins matching rules`
- `chore(deps): update Go toolchain version`

### Code Style

- Standard Go conventions, enforced by `gofmt` and `golangci-lint` rather than manual review
- Stdlib-only by design (see `go.mod`) — think carefully before proposing a new dependency; this
  project exists specifically to avoid the dependency surface of the tool it replaces
- Keep packages focused and small; prefer a new file over a growing one
- Comments explain _why_ a decision was made (not what the code already says) - especially for
  process-supervision or signal-handling edge cases that aren't obvious from the code alone

## Branch Workflow

- **Never commit directly to `main`**
- Create feature branches from `main`
- Name branches descriptively (e.g., `feat/install-plugins`, `fix/sigterm-race`)
- All work must be submitted via Pull Request
- PRs require passing CI/CD checks

## Testing

Before submitting a PR:

```bash
go test ./... -race -cover
```

New behavior should come with a test; bug fixes should come with a test that fails before the fix
and passes after.

## CI/CD Pipeline

All PRs trigger automated CI/CD that includes:

- Pre-commit checks (formatting, linting, file hygiene)
- Go build and test suite
- Dependency vulnerability scanning (Trivy)
- AI code review

Your PR must pass all checks before it can be merged.

## Pull Request Process

1. Ensure your branch is up-to-date with `main`
2. Push your branch to your fork
3. Open a Pull Request against `main`
4. Fill in the PR template with:
   - Clear description of changes
   - Link to any related issues
   - Test plan
5. Wait for CI/CD checks to complete
6. Address any review feedback
7. Once approved and checks pass, a maintainer will merge your PR

## What to Contribute

We welcome contributions in these areas:

- Bug fixes
- Feature enhancements
- Documentation improvements
- Test coverage improvements
- Performance optimizations
- Security improvements

Please open an issue first if you're planning a major change to discuss the approach.

## Getting Help

- Open an issue for bug reports, feature requests, or questions
- Check existing issues and PRs to avoid duplicates

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
