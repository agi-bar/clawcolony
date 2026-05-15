#!/usr/bin/env python3
"""Discover and lightly judge Hugging Face datasets for visual reasoning tasks.

This script implements a small, dependency-free MVP for the workflow:
keyword search -> dataset metadata -> card link extraction -> sample five rows ->
schema mapping -> rule-based sample judge -> JSONL reports.

It intentionally does not embed API keys or call an LLM. The rule judge output is
shaped so a later LLM judge can be inserted after `normalized_samples.jsonl` is
produced.
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from collections import Counter, defaultdict
from dataclasses import dataclass
from hashlib import md5
from typing import Any, Dict, Iterable, List, Optional, Sequence, Tuple

HF_API_BASE = "https://huggingface.co/api"
HF_DATASETS_SERVER_BASE = "https://datasets-server.huggingface.co"
DEFAULT_KEYWORDS = [
    "dot matrix",
    "dot-matrix font",
    "pixel font",
    "bitmap font",
    "grid pattern recognition",
    "glyph recognition",
    "OCR dot matrix",
    "seven segment",
    "LCD digits",
    "shadow matching",
    "shadow detection",
    "image matching",
    "line tracing",
    "curve tracing",
    "contour tracing",
    "edge tracing",
    "wireframe",
    "sketch understanding",
    "diagram understanding",
    "vectorization",
]

QUESTION_CANDIDATES = {
    "question",
    "query",
    "prompt",
    "instruction",
    "input",
    "problem",
    "task",
    "text",
    "caption",
    "description",
}
ANSWER_CANDIDATES = {
    "answer",
    "response",
    "output",
    "label",
    "labels",
    "target",
    "completion",
    "solution",
    "ground_truth",
    "gt",
    "annotation",
}
MULTIMODAL_CANDIDATES = {
    "image",
    "images",
    "img",
    "picture",
    "file",
    "path",
    "url",
    "image_url",
    "bytes",
}
COMMERCIAL_FRIENDLY_LICENSES = {
    "apache-2.0",
    "mit",
    "bsd",
    "bsd-2-clause",
    "bsd-3-clause",
    "cc-by-4.0",
    "cc-by-3.0",
    "cc0-1.0",
    "odc-by",
    "openrail",
}
NON_COMMERCIAL_MARKERS = (
    "cc-by-nc",
    "non-commercial",
    "non commercial",
    "noncommercial",
    "research only",
    "academic only",
)
SYSTEM_PROMPT_KEYWORDS = (
    "system:",
    "system prompt:",
    "you are chatgpt",
    "you are an ai assistant",
    "assistant:",
    "user:",
    "human:",
    "bot:",
)
DIRTY_KEYWORDS = (
    "porn",
    "sexual",
    "nude",
    "terrorism",
    "bomb making",
    "malware",
    "phishing",
    "credit card dump",
    "drug trafficking",
)
URL_RE = re.compile(r"https?://[^\s)\]}>\"']+", re.IGNORECASE)


@dataclass(frozen=True)
class HttpConfig:
    timeout: int
    retry: int
    sleep: float
    token: str = ""


class DiscoveryError(Exception):
    """Recoverable discovery failure for one dataset or endpoint."""


def read_keywords(args: argparse.Namespace) -> List[str]:
    keywords: List[str] = []
    if args.keywords:
        keywords.extend(args.keywords)
    if args.keywords_file:
        with open(args.keywords_file, "r", encoding="utf-8") as f:
            keywords.extend(line.strip() for line in f if line.strip() and not line.startswith("#"))
    if not keywords:
        keywords = list(DEFAULT_KEYWORDS)
    return sorted(dict.fromkeys(keywords))


def http_get_json(url: str, config: HttpConfig) -> Any:
    headers = {"User-Agent": "clawcolony-hf-dataset-discovery/0.1"}
    if config.token:
        headers["Authorization"] = f"Bearer {config.token}"
    last_error: Optional[BaseException] = None
    for attempt in range(config.retry + 1):
        try:
            request = urllib.request.Request(url, headers=headers)
            with urllib.request.urlopen(request, timeout=config.timeout) as response:
                raw = response.read().decode("utf-8")
            return json.loads(raw)
        except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError, json.JSONDecodeError) as exc:
            last_error = exc
            if attempt < config.retry:
                time.sleep(config.sleep)
    raise DiscoveryError(f"GET JSON failed for {url}: {last_error}")


def http_get_text(url: str, config: HttpConfig) -> str:
    headers = {"User-Agent": "clawcolony-hf-dataset-discovery/0.1"}
    if config.token:
        headers["Authorization"] = f"Bearer {config.token}"
    last_error: Optional[BaseException] = None
    for attempt in range(config.retry + 1):
        try:
            request = urllib.request.Request(url, headers=headers)
            with urllib.request.urlopen(request, timeout=config.timeout) as response:
                return response.read().decode("utf-8", errors="replace")
        except (urllib.error.URLError, urllib.error.HTTPError, TimeoutError) as exc:
            last_error = exc
            if attempt < config.retry:
                time.sleep(config.sleep)
    raise DiscoveryError(f"GET text failed for {url}: {last_error}")


def search_datasets(keyword: str, limit: int, config: HttpConfig) -> List[Dict[str, Any]]:
    query = urllib.parse.urlencode({"search": keyword, "limit": str(limit), "full": "true"})
    url = f"{HF_API_BASE}/datasets?{query}"
    data = http_get_json(url, config)
    return data if isinstance(data, list) else []


def dataset_info(dataset_id: str, config: HttpConfig) -> Dict[str, Any]:
    encoded = urllib.parse.quote(dataset_id, safe="/")
    return http_get_json(f"{HF_API_BASE}/datasets/{encoded}", config)


def dataset_card(dataset_id: str, config: HttpConfig) -> str:
    encoded = urllib.parse.quote(dataset_id, safe="/")
    for revision in ("main", "master"):
        url = f"https://huggingface.co/datasets/{encoded}/raw/{revision}/README.md"
        try:
            return http_get_text(url, config)
        except DiscoveryError:
            continue
    return ""


def dataset_splits(dataset_id: str, config: HttpConfig) -> Tuple[List[Dict[str, Any]], Optional[str]]:
    query = urllib.parse.urlencode({"dataset": dataset_id})
    try:
        data = http_get_json(f"{HF_DATASETS_SERVER_BASE}/splits?{query}", config)
    except DiscoveryError as exc:
        return [], str(exc)
    splits = data.get("splits", []) if isinstance(data, dict) else []
    return splits if isinstance(splits, list) else [], None


def first_rows(dataset_id: str, config_name: str, split: str, sample_size: int, config: HttpConfig) -> Tuple[List[Dict[str, Any]], List[Dict[str, Any]], Optional[str]]:
    query = urllib.parse.urlencode(
        {"dataset": dataset_id, "config": config_name, "split": split, "offset": "0", "length": str(sample_size)}
    )
    try:
        data = http_get_json(f"{HF_DATASETS_SERVER_BASE}/rows?{query}", config)
    except DiscoveryError as exc:
        return [], [], str(exc)
    rows = data.get("rows", []) if isinstance(data, dict) else []
    features = data.get("features", []) if isinstance(data, dict) else []
    if not isinstance(rows, list):
        rows = []
    if not isinstance(features, list):
        features = []
    return [row.get("row", {}) for row in rows if isinstance(row, dict)], features, None


def classify_license(raw_license: str, tags: Sequence[str], card_text: str) -> Dict[str, str]:
    candidates = [raw_license, *tags]
    normalized = [str(item).lower().replace("license:", "").strip() for item in candidates if item]
    joined = " ".join(normalized + [card_text[:5000].lower()])
    if any(marker in joined for marker in NON_COMMERCIAL_MARKERS):
        return {"commercial_use": "no", "license_risk": "high"}
    if any(item in COMMERCIAL_FRIENDLY_LICENSES for item in normalized):
        return {"commercial_use": "yes", "license_risk": "low"}
    if raw_license:
        return {"commercial_use": "unknown", "license_risk": "medium"}
    return {"commercial_use": "unknown", "license_risk": "unknown"}


def extract_links(card_text: str, info: Dict[str, Any]) -> Dict[str, List[str]]:
    text_parts = [card_text]
    for key in ("homepage", "paper", "citation", "dataset_info"):
        value = info.get(key)
        if value:
            text_parts.append(json.dumps(value, ensure_ascii=False) if not isinstance(value, str) else value)
    text = "\n".join(text_parts)
    all_links = sorted(dict.fromkeys(URL_RE.findall(text)))
    github_links = [url for url in all_links if "github.com" in url.lower()]
    arxiv_links = [url for url in all_links if "arxiv.org" in url.lower()]
    paper_links = [url for url in all_links if any(host in url.lower() for host in ("doi.org", "openaccess.thecvf.com", "aclanthology.org"))]
    codebase_links = []
    homepage_links = []
    for url in all_links:
        lower = url.lower()
        if "github.com" in lower or any(word in lower for word in ("code", "repo", "repository", "implementation")):
            codebase_links.append(url)
        elif "huggingface.co" not in lower and "arxiv.org" not in lower:
            homepage_links.append(url)
    return {
        "all_links": all_links,
        "github_links": sorted(dict.fromkeys(github_links)),
        "arxiv_links": sorted(dict.fromkeys(arxiv_links)),
        "paper_links": sorted(dict.fromkeys(paper_links)),
        "codebase_links": sorted(dict.fromkeys(codebase_links)),
        "homepage_links": sorted(dict.fromkeys(homepage_links)),
    }


def normalize_feature_names(features: Sequence[Dict[str, Any]], rows: Sequence[Dict[str, Any]]) -> List[str]:
    names = []
    for feature in features:
        if isinstance(feature, dict) and feature.get("name"):
            names.append(str(feature["name"]))
    if not names and rows:
        names = [str(k) for k in rows[0].keys()]
    return sorted(dict.fromkeys(names))


def key_score(name: str, candidates: set[str]) -> int:
    lower = name.lower()
    if lower in candidates:
        return 100 - len(lower)
    if any(candidate in lower for candidate in candidates):
        return 10 - len(lower)
    return -1


def pick_keys(names: Sequence[str], candidates: set[str], max_count: int = 2) -> List[str]:
    scored = [(key_score(name, candidates), name) for name in names]
    picked = [name for score, name in sorted(scored, reverse=True) if score >= 0]
    return picked[:max_count]


def infer_schema_mapping(features: Sequence[Dict[str, Any]], rows: Sequence[Dict[str, Any]]) -> Dict[str, Any]:
    names = normalize_feature_names(features, rows)
    multimodal = pick_keys(names, MULTIMODAL_CANDIDATES, max_count=3)
    text_names = [name for name in names if name not in set(multimodal)]
    question = pick_keys(text_names, QUESTION_CANDIDATES, max_count=2)
    answer = pick_keys([name for name in text_names if name not in set(question)], ANSWER_CANDIDATES, max_count=2)
    status = "success" if (question or multimodal) and answer else "partial" if names else "unknown"
    return {
        "schema_mapping_status": status,
        "question_key": question,
        "answer_key": answer,
        "multimodal_key": multimodal,
        "feature_names": names,
    }


def value_to_text(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, (str, int, float, bool)):
        return str(value)
    if isinstance(value, bytes):
        return "<bytes>"
    return json.dumps(value, ensure_ascii=False, sort_keys=True)


def extract_by_keys(row: Dict[str, Any], keys: Sequence[str]) -> str:
    return "\n".join(value_to_text(row.get(key, "")) for key in keys if key in row).strip()


def qa_hash(question: str, answer: str) -> str:
    normalized = re.sub(r"\s+", " ", f"{question} ||| {answer}".lower()).strip()
    return md5(normalized.encode("utf-8")).hexdigest() if normalized else ""


def rule_judge_sample(sample: Dict[str, Any]) -> Dict[str, Any]:
    question = extract_by_keys(sample["data_content"], sample["question_key"])
    answer = extract_by_keys(sample["data_content"], sample["answer_key"])
    combined = f"{question}\n{answer}".lower()
    empty = 1 if not question or not answer else 0
    system_prompt = 1 if any(keyword in combined for keyword in SYSTEM_PROMPT_KEYWORDS) else 0
    dirty = 1 if any(keyword in combined for keyword in DIRTY_KEYWORDS) else 0
    qa_pair_fail = 1 if empty else 0
    return {
        "question_chars": len(question),
        "answer_chars": len(answer),
        "空值检测": empty,
        "System Prompt检测": system_prompt,
        "规则脏数据量": dirty,
        "规则QA检测失败": qa_pair_fail,
        "qa_hash": qa_hash(question, answer),
    }


def build_normalized_samples(dataset_id: str, rows: Sequence[Dict[str, Any]], mapping: Dict[str, Any], split: str) -> List[Dict[str, Any]]:
    samples = []
    for index, row in enumerate(rows):
        if not isinstance(row, dict):
            continue
        sample = {
            "dataset_id": dataset_id,
            "case_id": f"{dataset_id.replace('/', '__')}__{split}__{index}",
            "split": split,
            "row_index": index,
            "question_key": mapping.get("question_key", []),
            "answer_key": mapping.get("answer_key", []),
            "multimodal_key": mapping.get("multimodal_key", []),
            "data_content": row,
        }
        sample["rule_judge"] = rule_judge_sample(sample)
        samples.append(sample)
    return samples


def write_jsonl(path: str, rows: Iterable[Dict[str, Any]]) -> int:
    count = 0
    with open(path, "w", encoding="utf-8") as f:
        for row in rows:
            f.write(json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n")
            count += 1
    return count


def append_jsonl(path: str, row: Dict[str, Any]) -> None:
    with open(path, "a", encoding="utf-8") as f:
        f.write(json.dumps(row, ensure_ascii=False, sort_keys=True) + "\n")


def summarize_samples(samples: Sequence[Dict[str, Any]]) -> Dict[str, Any]:
    counters = Counter()
    hashes = set()
    for sample in samples:
        judge = sample.get("rule_judge", {})
        counters["empty_count"] += int(judge.get("空值检测", 0))
        counters["system_prompt_count"] += int(judge.get("System Prompt检测", 0))
        counters["rule_dirty_count"] += int(judge.get("规则脏数据量", 0))
        counters["rule_qa_fail_count"] += int(judge.get("规则QA检测失败", 0))
        if judge.get("qa_hash"):
            hashes.add(judge["qa_hash"])
    sample_count = len(samples)
    pass_count = sample_count - counters["empty_count"] - counters["rule_dirty_count"] - counters["rule_qa_fail_count"]
    recommendation = "keep" if sample_count and pass_count >= max(1, sample_count - 1) else "review" if sample_count else "drop"
    return {
        "sample_count": sample_count,
        "unique_rule_qa_hashes": len(hashes),
        "rule_pass_count": max(0, pass_count),
        "recommendation": recommendation,
        **dict(counters),
    }


def choose_split(splits: Sequence[Dict[str, Any]]) -> Tuple[str, str]:
    if not splits:
        return "default", "train"
    preferred = ["train", "validation", "test"]
    for preferred_split in preferred:
        for split in splits:
            if split.get("split") == preferred_split:
                return str(split.get("config", "default")), preferred_split
    split = splits[0]
    return str(split.get("config", "default")), str(split.get("split", "train"))


def compact_dataset_record(search_hit: Dict[str, Any], info: Dict[str, Any], card_text: str, keyword_hits: Sequence[str]) -> Dict[str, Any]:
    dataset_id = str(info.get("id") or search_hit.get("id") or search_hit.get("_id") or "")
    tags = info.get("tags") or search_hit.get("tags") or []
    if not isinstance(tags, list):
        tags = []
    card_data = info.get("cardData") or {}
    if not isinstance(card_data, dict):
        card_data = {}
    raw_license = str(card_data.get("license") or info.get("license") or "")
    if not raw_license:
        for tag in tags:
            if str(tag).lower().startswith("license:"):
                raw_license = str(tag).split(":", 1)[1]
                break
    links = extract_links(card_text, info)
    license_info = classify_license(raw_license, tags, card_text)
    return {
        "dataset_id": dataset_id,
        "hf_url": f"https://huggingface.co/datasets/{dataset_id}",
        "search_keywords_hit": sorted(dict.fromkeys(keyword_hits)),
        "downloads": info.get("downloads") or search_hit.get("downloads"),
        "likes": info.get("likes") or search_hit.get("likes"),
        "last_modified": info.get("lastModified") or search_hit.get("lastModified"),
        "created_at": info.get("createdAt") or search_hit.get("createdAt"),
        "tags": tags,
        "license": raw_license,
        "card_metadata": card_data,
        **license_info,
        **links,
    }


def discover(args: argparse.Namespace) -> Dict[str, int]:
    os.makedirs(args.output_dir, exist_ok=True)
    metadata_path = os.path.join(args.output_dir, "datasets_metadata.jsonl")
    samples_path = os.path.join(args.output_dir, "normalized_samples.jsonl")
    summary_path = os.path.join(args.output_dir, "datasets_judge_summary.jsonl")
    for path in (metadata_path, samples_path, summary_path):
        open(path, "w", encoding="utf-8").close()

    config = HttpConfig(timeout=args.timeout, retry=args.retry, sleep=args.retry_sleep, token=args.hf_token or os.getenv("HF_TOKEN", ""))
    keywords = read_keywords(args)
    hits_by_id: Dict[str, Dict[str, Any]] = {}
    keywords_by_id: Dict[str, List[str]] = defaultdict(list)

    search_errors: List[Dict[str, str]] = []
    for keyword in keywords:
        try:
            hits = search_datasets(keyword, args.max_datasets_per_keyword, config)
        except DiscoveryError as exc:
            search_errors.append({"keyword": keyword, "error": str(exc)})
            continue
        for hit in hits:
            dataset_id = str(hit.get("id") or hit.get("_id") or "")
            if not dataset_id:
                continue
            hits_by_id.setdefault(dataset_id, hit)
            keywords_by_id[dataset_id].append(keyword)

    errors_path = os.path.join(args.output_dir, "discovery_errors.jsonl")
    write_jsonl(errors_path, search_errors)

    processed = 0
    sample_total = 0
    for dataset_id in sorted(hits_by_id):
        try:
            info = dataset_info(dataset_id, config)
            card_text = dataset_card(dataset_id, config)
            record = compact_dataset_record(hits_by_id[dataset_id], info, card_text, keywords_by_id[dataset_id])
            splits, split_error = dataset_splits(dataset_id, config)
            config_name, split_name = choose_split(splits)
            rows, features, sample_error = first_rows(dataset_id, config_name, split_name, args.sample_size, config)
            mapping = infer_schema_mapping(features, rows)
            samples = build_normalized_samples(dataset_id, rows, mapping, split_name)
            sample_summary = summarize_samples(samples)
            record.update(
                {
                    "configs": sorted({str(item.get("config", "default")) for item in splits}) if splits else [],
                    "splits": splits,
                    "selected_config": config_name,
                    "selected_split": split_name,
                    "features": features,
                    "schema_mapping": mapping,
                    "sample_load_status": "success" if samples else "failed",
                    "sample_error": sample_error or split_error or "",
                    **sample_summary,
                }
            )
            append_jsonl(metadata_path, record)
            summary_row = {
                "dataset_id": dataset_id,
                "hf_url": record["hf_url"],
                "search_keywords_hit": record["search_keywords_hit"],
                "license": record["license"],
                "commercial_use": record["commercial_use"],
                "license_risk": record["license_risk"],
                "github_links": record["github_links"],
                "homepage_links": record["homepage_links"],
                "codebase_links": record["codebase_links"],
                "schema_mapping": mapping,
                **sample_summary,
            }
            append_jsonl(summary_path, summary_row)
            for sample in samples:
                append_jsonl(samples_path, sample)
            processed += 1
            sample_total += len(samples)
        except DiscoveryError as exc:
            append_jsonl(
                metadata_path,
                {
                    "dataset_id": dataset_id,
                    "hf_url": f"https://huggingface.co/datasets/{dataset_id}",
                    "search_keywords_hit": sorted(dict.fromkeys(keywords_by_id[dataset_id])),
                    "sample_load_status": "failed",
                    "sample_error": str(exc),
                },
            )
            processed += 1

    return {"datasets": processed, "samples": sample_total, "keywords": len(keywords), "search_errors": len(search_errors)}


def self_test() -> None:
    row = {"image": "https://example.test/a.png", "question": "Trace the line", "answer": "line A"}
    features = [{"name": "image"}, {"name": "question"}, {"name": "answer"}]
    mapping = infer_schema_mapping(features, [row])
    assert mapping["question_key"] == ["question"], mapping
    assert mapping["answer_key"] == ["answer"], mapping
    assert mapping["multimodal_key"] == ["image"], mapping
    samples = build_normalized_samples("owner/name", [row], mapping, "train")
    assert samples[0]["rule_judge"]["空值检测"] == 0, samples
    license_info = classify_license("cc-by-nc-4.0", [], "")
    assert license_info["commercial_use"] == "no", license_info
    links = extract_links("Code: https://github.com/example/repo Paper: https://arxiv.org/abs/1234.5678", {})
    assert links["github_links"] == ["https://github.com/example/repo"], links
    print("self-test passed")


def build_arg_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="Discover HF datasets and run a five-row rule judge MVP.")
    parser.add_argument("--keywords", nargs="*", help="Search keywords. Defaults to the built-in visual-reasoning matrix.")
    parser.add_argument("--keywords-file", help="One keyword per line; comments beginning with # are ignored.")
    parser.add_argument("--output-dir", default="artifacts/hf_dataset_discovery", help="Output directory for JSONL reports.")
    parser.add_argument("--max-datasets-per-keyword", type=int, default=10, help="HF search result limit per keyword.")
    parser.add_argument("--sample-size", type=int, default=5, help="Rows to fetch from the selected split for each dataset.")
    parser.add_argument("--timeout", type=int, default=30, help="HTTP timeout in seconds.")
    parser.add_argument("--retry", type=int, default=1, help="HTTP retry count after the first attempt.")
    parser.add_argument("--retry-sleep", type=float, default=1.0, help="Sleep seconds between retries.")
    parser.add_argument("--hf-token", default="", help="Optional HF token. Prefer HF_TOKEN env var over CLI history.")
    parser.add_argument("--self-test", action="store_true", help="Run local self-tests without network access.")
    return parser


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = build_arg_parser()
    args = parser.parse_args(argv)
    if args.self_test:
        self_test()
        return 0
    stats = discover(args)
    print(json.dumps(stats, ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
