# Contributing to BYOI Platform

Thank you for considering contributing to BYOI Platform! This document provides guidelines and instructions for contributing to the project.

## Code of Conduct

Be respectful, inclusive, and constructive in all interactions. We're building this together.

## Getting Started

### Prerequisites

- **Go**: Version 1.21 or higher
- **Rust**: Latest stable (for Soroban contracts)
- **Stellar CLI**: For smart contract development
- **Docker**: For local testing (optional)
- **Git**: For version control

### Development Setup

1. **Clone the repository**
   ```bash
   git clone https://github.com/yourusername/byoi-platform.git
   cd byoi-platform
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
   # Go tests
   go test ./...
   
   # Rust tests
   cd contracts && cargo test
   ```

5. **Build binaries**
   ```bash
   # CLI
   cd apps/cli && go build -o ../../bin/byoi-cli
   
   # Agent
   cd apps/agent && go build -o ../../bin/byoi-agent
   
   # Control Plane
   cd apps/control-plane && go build -o ../../bin/byoi-control
   ```

## Development Workflow

### Branching Strategy

- `main`: Stable, production-ready code
- `develop`: Integration branch for features (if needed)
- `feature/*`: New features
- `fix/*`: Bug fixes
- `docs/*`: Documentation updates

### Making Changes

1. **Create a branch**
   ```bash
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
   go test ./...
   
   # Format Rust code
   cd contracts && cargo fmt
   
   # Run Rust tests
   cargo test
   ```

4. **Commit your changes**
   ```bash
   git add .
   git commit -m "feat: add new feature"
   ```
   
   Use conventional commits:
   - `feat:` New feature
   - `fix:` Bug fix
   - `docs:` Documentation changes
   - `refactor:` Code refactoring
   - `test:` Adding or updating tests
   - `chore:` Maintenance tasks

5. **Push and create a pull request**
   ```bash
   git push origin feature/your-feature-name
   ```
   
   Then create a PR on GitHub with:
   - Clear description of changes
   - Link to related issues
   - Screenshots/examples if applicable

## Code Style Guidelines

### Go

- Follow standard Go conventions and idioms
- Use `gofmt` and `goimports` for formatting
- Write meaningful variable and function names
- Add comments for exported functions and types
- Keep functions focused and reasonably sized
- Use table-driven tests where appropriate
- Handle errors explicitly, don't ignore them

**Example:**
```go
// ProcessPayment handles payment through Stellar network
func ProcessPayment(ctx context.Context, amount int64) error {
    if amount <= 0 {
        return fmt.Errorf("invalid amount: %d", amount)
    }
    // Implementation
    return nil
}
```

### Rust (Soroban Contracts)

- Follow Rust standard style (`cargo fmt`)
- Use `clippy` for additional linting
- Prefer explicit error handling
- Write comprehensive tests for contract functions
- Document contract interfaces thoroughly
- Keep contracts simple and auditable

**Example:**
```rust
/// Processes an escrow payment between parties
pub fn process_escrow(env: Env, amount: i128, recipient: Address) -> Result<(), Error> {
    if amount <= 0 {
        return Err(Error::InvalidAmount);
    }
    // Implementation
    Ok(())
}
```

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
   - Ensure all tests pass
   - Run linters and formatters
   - Update documentation
   - Rebase on latest `main` if needed

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

## Project Status Note

BYOI Platform is currently in **early MVP stage**. This means:

- Architecture and APIs may change significantly
- Not all features are implemented yet
- Breaking changes may occur between versions
- We're especially open to feedback on design decisions

Your patience and contributions during this phase are greatly appreciated!

## Questions or Need Help?

- Open a [GitHub Discussion](https://github.com/yourusername/byoi-platform/discussions)
- Check existing issues and PRs
- Reach out to maintainers in discussions

## Recognition

Contributors will be recognized in release notes and the project README. Thank you for helping build BYOI Platform!
