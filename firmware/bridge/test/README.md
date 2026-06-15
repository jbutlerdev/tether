# Tether RAK4631 bridge — tests

This directory is the test root for the PlatformIO `unity` test runner.

## Running tests

```bash
cd firmware/bridge
pio test
```

The `unity` framework is registered via `lib/Unity`. Test sources use the
pattern `test_<name>.cpp` and are compiled for the host (`native`) by default
in CI; the actual `rak4631` hardware environment is exercised manually on a
bench.

See `plan.md` §1.1 and §0.2 for the convention.
