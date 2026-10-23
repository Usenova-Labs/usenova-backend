# Contributing to Usenova

Thank you for considering contributing to Usenova! This document provides guidelines
and instructions for contributing to the project.

## Code of Conduct

Be respectful, inclusive, and constructive in all interactions. We're building this together.

## Getting Started

### Prerequisites

- **Go**: Version 1.21 or higher
- **Rust**: Latest stable (for Soroban contracts)
- **Stellar CLI**: For smart contract development (`cargo install --locked soroban-cli`)
- **Docker**: For local testing (the agent manages Docker containers)
- **Git**: For version control

### Development Setup

1. **Clone the repository**
   ```bash
   git clone https://github.com/Usenova-Labs/usenova-backend.git
   cd usenova-backend
   ```

2. **Install Go dependencies**
   ```bash
   go work sync
   cd apps/cli && go mod download
   cd ../agent && go mod download
   cd ../control-plane && go mod download
   ```

3. **Install Rust toolchain and Soroban CLI**
   ```bash
   rustup update
   cargo install --locked soroban-cli
   ```

4. **Run tests**
   ```bash
   # All Go tests (CLI, agent, control plane, shared packages)
   ./scripts/test.sh

   # Or per module
   cd apps/cli && go test -race ./...
   cd apps/agent && go test -race ./...
   cd apps/control-plane && go test -race ./...
   cd packages/stellar && go test -race ./...

   # Rust / Soroban contract tests
   cd contracts && cargo test
   ```

5. **Build binaries**
   ```bash
   ./scripts/build.sh
   # Or individually:
   (cd apps/cli && go build -o ../../bin/nova)
   (cd apps/agent && go build -o ../../bin/usenova-agent)
   (cd apps/control-plane && go build -o ../../bin/nova-control)
   ```

6. **Run the local stack**

   See [docs/getting-started.md](docs/getting-started.md) for a step-by-step walkthrough,
   or run the automated smoke test:

   ```bash
   ./scripts/e2e-smoke.sh
   ```

## Development Workflow

### Branching Strategy

- **`develop`** is the default integration branch. Branch from `develop` and PR into `develop`.
- `feature/*`: New features
- `fix/*`: Bug fixes
- `docs/*`: Documentation updates
- `chore/*`: Maintenance tasks

### Making Changes

1. **Create a branch from `develop`**
   ```bash
   git checkout develop
   git pull origin develop
   git checkout -b feature/your-feature-name
   ```

2. **Make your changes**
   - Write clear, well-documented code
   - Follow the code style guidelines below
   - Add tests for new functionality
   - Update documentation as needed

3. **Run tests and linting**
   ```bash
   # Format Go code
   gofmt -w .
   goimports -w .

   # Run Go tests
   go test -race ./...

   # Format Rust code
   cd contracts && cargo fmt

   # Run Rust tests
   cargo test
   ```

4. **Commit your changes**
   ```bash
   git add <specific-files>
   git commit -m "feat: add new feature"
   ```

   Use conventional commits:
   - `feat:` New feature
   - `fix:` Bug fix
   - `docs:` Documentation changes
   - `refactor:` Code refactoring
   - `test:` Adding or updating tests
   - `chore:` Maintenance tasks

5. **Push and create a pull request into `develop`**
   ```bash
   git push -u origin feature/your-feature-name
   ```

   Then create a PR on GitHub with:
   - Clear description of changes
   - Link to related issues
   - Screenshots/examples if applicable

## Issue Labels

| Label | Meaning |
| --- | --- |
| `good first issue` | Scoped, beginner-friendly — ideal for first PRs |
| `stellar` | Stellar network / Soroban contract work |
| `agent` | Agent daemon (`apps/agent`) |
| `control-plane` | Control plane server (`apps/control-plane`) |
| `cli` | Nova CLI (`apps/cli`) |
| `docs` | Documentation |
| `mvp` | Core MVP scope |
| `reliability` | Reliability / circuit breaker / restart logic |
| `security` | Security-related |

## Stellar Wave Contributors

Usenova participates in the [Stellar Wave Program on Drips](https://docs.drips.network/wave/).
If you are a Wave contributor:

1. **Only work issues you are assigned** via Drips or GitHub. Open a comment on the issue
   asking to be assigned before starting.
2. **Open a PR** linking the issue (`Closes #NN`) once your work is ready for review.
3. **Keep the PR scope tight** — one issue per PR unless explicitly agreed otherwise.

Wave is a community program. It does not imply SDF endorsement of this project, and the
Wave reward is compensation for your contribution, not a grant to the project.

## Code Style Guidelines

### Go

- Follow standard Go conventions and idioms
- Use `gofmt` and `goimports` for formatting
- Write meaningful variable and function names
- Add comments for exported functions and types
- Keep functions focused and reasonably sized
- Use table-driven tests where appropriate
- Handle errors explicitly, don't ignore them

### Rust (Soroban Contracts)

- Follow Rust standard style (`cargo fmt`)
- Use `clippy` for additional linting
- Prefer explicit error handling
- Write comprehensive tests for contract functions
- Document contract interfaces thoroughly
- Keep contracts simple and auditable

## Testing

- Write tests for all new functionality
- Maintain or improve code coverage
- Include both unit tests and integration tests where applicable
- Test edge cases and error conditions
- Use meaningful test names that describe what is being tested

## Documentation

- Update README.md for user-facing changes
- Add/update inline code comments for complex logic
- Document API endpoints and contract interfaces
- Update architecture docs in `docs/` for structural changes

## Pull Request Process

1. **Before submitting:**
   - Ensure all tests pass (`./scripts/test.sh`)
   - Run linters and formatters
   - Update documentation
   - Rebase on latest `develop` if needed

2. **PR requirements:**
   - Clear, descriptive title
   - Detailed description of changes
   - Link to related issues
   - All CI checks passing

3. **Review process:**
   - At least one approving review required
   - Address reviewer feedback promptly
   - Keep discussions professional and constructive

4. **Merging:**
   - Squash commits if there are many small fixup commits
   - Maintainers will merge when approved and CI passes

## Project Status

Usenova is in active development. The BYOI (Bring Your Own Infrastructure) path works end
to end today: CLI, agent, control plane, Docker deploys, health checks, circuit breaker,
remote deploy command channel, Stellar testnet linking, and Soroban payment escrow.

Architecture and APIs may change as the project evolves. Breaking changes may occur
between versions. We're especially open to feedback on design decisions.

## Questions or Need Help?

- Open a [GitHub Discussion](https://github.com/Usenova-Labs/usenova-backend/discussions)
- Check existing issues and PRs
- Reach out to maintainers in discussions

## Recognition

Contributors will be recognized in release notes and the project README. Thank you for
helping build Usenova!
