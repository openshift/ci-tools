# clusterimageset-updater integration test

This suite runs `clusterimageset-updater` against fixture pool and imageset YAML under `input/`, then compares the result to `output/`.

The test uses `mock-release-server` to serve a fixed OCP release response instead of calling the live release catalog. That keeps the suite stable when new z-streams ship; only change `output/` if the test scenario itself changes.

The mocked pullspec must stay in sync with the expected `output/` fixtures (currently `4.21.25-multi`).
