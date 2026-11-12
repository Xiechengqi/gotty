# GitHub Actions policy

- Do not add or run UI tests in GitHub Actions, including frontend component/DOM tests, browser automation, screenshots, or end-to-end UI tests.
- Do not invoke `make test-js`, `make test-e2e`, `npm test`, Vitest, Playwright, or browser regression scripts from workflows, including through reusable workflows or build scripts.
- Keep dependency installation, type checking, asset generation/consistency checks, backend tests, and release builds in CI.
- UI regression tests may remain in the repository for use in an explicitly authorized isolated test environment outside GitHub Actions. Do not run them against a live GoTTY service.
