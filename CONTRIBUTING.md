# Contributing to Mercato

Thank you for your interest in contributing to Mercato! We appreciate your help in making this project better.

## Vision
Mercato's goal is to be a zero-friction, privacy-first, real-time shared shopping list. We value simplicity, performance, and accessibility.

## How to Contribute

### Reporting Bugs
- Use the **Bug Report** template when opening an issue.
- Provide a clear, descriptive title.
- Include steps to reproduce the issue.
- Describe the expected behavior and what actually happened.

### Suggesting Features
- Use the **Feature Request** template.
- Explain the problem this feature solves and why it's a good fit for Mercato.

### Pull Requests
1. Fork the repository.
2. Create a new branch for your changes (`git checkout -b feature/cool-new-thing`).
3. Ensure your code follows the existing style.
4. Add or update tests as necessary.
5. Run `make build` and `make test` to verify your changes.
6. Submit a PR using the **Pull Request Template**.

## Development Setup

### Prerequisites
- Go 1.25 or higher
- [templ](https://github.com/a-h/templ) (`go install github.com/a-h/templ/cmd/templ@latest`)

### Getting Started
1. Clone the repo: `git clone https://github.com/PrathxmOp/Mercato.git`
2. Install dependencies: `go mod tidy`
3. Generate templates: `templ generate`
4. Run the app: `go run cmd/mercato/main.go`

## Coding Standards
- Follow idiomatic Go patterns.
- Keep components small and focused in `view/components`.
- Use localized strings via `i18n.T` for all user-facing text.
- Maintain a mobile-first design approach.

## Code of Conduct
Please be respectful and inclusive in all interactions related to this project.
