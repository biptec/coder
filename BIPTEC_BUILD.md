# Biptec Coder image build

This fork builds a custom Coder Docker image and pushes it to GitHub Container Registry.

## Image repository

```text
ghcr.io/biptec/coder
```

## Tags

The main deploy tag is based on the commit SHA:

```text
ghcr.io/biptec/coder:sha-<short-sha>
```

When pushed to `main`, the workflow also updates:

```text
ghcr.io/biptec/coder:main
```

For Kubernetes deploys, prefer the immutable SHA tag instead of `main`.

## Workflow

The workflow is here:

```text
.github/workflows/build-custom-image.yaml
```

It runs on:

```text
push to main
manual workflow_dispatch
```

Manual runs can also create an extra tag, for example:

```text
dev-001
```

## dev-infra values

Use the pushed image in `dev-infra` Coder Helm values:

```yaml
coder:
  image:
    repo: ghcr.io/biptec/coder
    tag: sha-<short-sha>
```

Exact Helm value names should be confirmed in `dev-infra` when wiring the Coder chart.
