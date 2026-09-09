# models/ — model manifests (NC1)

Model **weights never enter Git** (ROADMAP §4: 权重引用外部存储). What
lives here are the JSON **manifests** that travel with each exported ONNX
model, plus this convention document. The runtime trusts the manifest for
labels, input geometry (`imgsz`) and the default confidence threshold —
never for weights.

## Files

- `example.manifest.json` — schema v1 example for the dry-run probe
  detector; validate any manifest with:
  ```text
  controller.exe --manifest-check path/to/model.manifest.json
  ```

## Manifest schema v1

| field                | required | meaning                                             |
| -------------------- | -------- | --------------------------------------------------- |
| `schema_version`     | yes      | must be `1` (unknown versions are rejected)         |
| `name`               | yes      | model name, non-blank                               |
| `version`            | yes      | model version string (not the schema version)       |
| `game_profile`       | yes      | target game/profile, e.g. `generic-ui`              |
| `input_size`         | yes      | `{width, height}` in 1..=4096 — the letterbox target |
| `labels`             | yes      | 1..=1024 unique, non-blank class names              |
| `default_confidence` | yes      | threshold in (0, 1]; operator can override per run  |
| `weights`            | no       | external reference to the weights file (path/URI)   |
| `notes`              | no       | free text                                           |

Precedence when running: an explicit `--model` / `--min-confidence` beats
the manifest; otherwise the manifest's `input_size` / `default_confidence`
drive the session.

## Ignored patterns

`*.onnx` is git-ignored repo-wide. Exported weights belong on external
storage; only their manifests are committed.
