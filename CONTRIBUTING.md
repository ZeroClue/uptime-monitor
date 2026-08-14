# Contributing

Thank you for contributing to the Uptime Monitor!

## Getting Started

1. Fork the repository
2. Create a feature branch: `git checkout -b feat/my-feature`
3. Make your changes
4. Run tests and lint: `make test lint`
5. Submit a pull request

## Development Setup

```bash
# Prerequisites: Go 1.22+, Docker, Make
make deps          # Download dependencies
make build         # Build binary
make test          # Run unit tests
make integration   # Run integration tests (requires Docker)
```

## Code Standards

- **Go**: Follow standard Go conventions (`gofmt`, `golint`, `go vet`)
- **Tests**: Write unit tests for new logic; integration tests for collectors
- **Commits**: Conventional commits (`feat:`, `fix:`, `docs:`, `refactor:`)
- **Documentation**: Update README and config examples for user-facing changes

## Pull Request Process

1. Ensure all checks pass (`make test lint`)
2. Update CHANGELOG.md if applicable
3. Request review from maintainers
4. Address feedback
5. Squash and merge on approval

## Reporting Issues

Use GitHub Issues with:
- Clear title and description
- Steps to reproduce
- Expected vs actual behavior
- Environment details (Go version, OS, Docker version)

## Security

Report security vulnerabilities privately via GitHub Security Advisories.