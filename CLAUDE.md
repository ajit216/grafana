# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

@AGENTS.md

## Claude Code Notes

### Critical testing gotcha

`yarn test` runs in `--watch` mode by default. **Always use `yarn jest --no-watch`** to run tests once and exit:

```bash
yarn jest --no-watch path/to/file.test.tsx        # Run specific file once
yarn jest --no-watch -t "pattern"                 # Run by name pattern once
```

### Middleware patterns (`pkg/middleware/`)

Each middleware file follows a consistent pattern:
- Middleware functions take `*web.Context` and call `c.Next()` to pass the request
- Request tracing uses OpenTelemetry spans; enrich them via `span_enricher.go`
- Sampling decisions (probabilistic, IP-based, etc.) live in `sampling.go`
- New middleware is wired in `middleware.go` via the `Middleware` struct
