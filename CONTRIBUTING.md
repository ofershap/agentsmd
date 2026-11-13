# Contributing

Issues and pull requests are welcome.

- Keep the false-positive rate at zero for error-level rules: only flag
  references that are unambiguous.
- `go test ./...` and `golangci-lint run` must pass.
- New reference types (other build systems, other manifest files) should come
  with a test that demonstrates the drift and one that demonstrates no false
  positive on plain prose.
