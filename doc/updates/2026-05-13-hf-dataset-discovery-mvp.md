# 2026-05-13: Hugging Face Dataset Discovery MVP

## Summary

Added a standalone `scripts/hf_dataset_discovery.py` utility for discovering Hugging Face datasets related to visual reasoning/OCR-style tasks before deeper pass@5 or LLM judging work.

## What Changed

- Added keyword-matrix driven Hugging Face dataset search with a built-in seed list for dot-matrix fonts, pixel fonts, shadow matching, line tracing, sketch/diagram understanding, and vectorization tasks.
- Added dataset metadata capture for Hugging Face URL, downloads, likes, timestamps, tags, card metadata, license, commercial-use classification, configs, splits, and dataset-server features.
- Added Dataset Card/README link extraction for GitHub, codebase-like links, project homepages, arXiv, and paper URLs.
- Added five-row sampling from the Hugging Face datasets server with conservative split selection (`train`, then `validation`, then `test`, then the first available split).
- Added schema inference to map sampled rows into the existing judge-friendly shape: `question_key`, `answer_key`, `multimodal_key`, and `data_content`.
- Added a dependency-free rule judge for sampled rows covering empty QA fields, system-prompt/role contamination, simple dirty-keyword flags, QA hash generation, and per-dataset summary counts.

## Why It Changed

The new research flow needs an initial dataset discovery plane before the existing dirty-data/QA judge can be applied. The MVP provides a safe, keyless first stage that collects candidate datasets and normalizes a tiny sample per dataset without embedding secrets or calling an LLM.

## How to Verify

```bash
python3 scripts/hf_dataset_discovery.py --self-test
python3 -m py_compile scripts/hf_dataset_discovery.py
```

A live smoke test can be run with a small keyword/limit:

```bash
python3 scripts/hf_dataset_discovery.py --keywords "dot matrix" --max-datasets-per-keyword 1 --sample-size 1 --output-dir /tmp/hf_dataset_discovery_smoke
```

## Visible Changes to Agents

No hosted runtime protocol changes are exposed to agents. Operators now have a local research utility that emits:

- `datasets_metadata.jsonl`
- `normalized_samples.jsonl`
- `datasets_judge_summary.jsonl`
- `discovery_errors.jsonl`

These files can feed the next judge stage after manual review or an environment-specific LLM client is attached.
