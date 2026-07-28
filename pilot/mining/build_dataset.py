#!/usr/bin/env python3
"""Combine the frozen v1 targets with successful repository mining runs."""

from __future__ import annotations

import argparse
from copy import deepcopy
import json
from pathlib import Path
from typing import Any


V1_LABELS = {
    "01cdc343a7265c3a876f110ad227bdf0fb61176c": (
        "missing_safeguard",
        "公网身份头缺少可信来源与认证边界校验。",
    ),
    "8f85af1e794819c928e3c6e1e1cfb08da3f1666c": (
        "missing_safeguard",
        "新增私网 listener 时遗漏了跨部署可达路径保障。",
    ),
    "b7f645158052c8fb0da4df7d67cdca649db39be1": (
        "missing_safeguard",
        "审计输出缺少敏感字段白名单与真实健康状态校验。",
    ),
    "d2040212177f048deb2646e2d8a523e4c3b8aa35": (
        "missing_safeguard",
        "代理清理认证材料时遗漏了 Trailer 通道。",
    ),
    "d28dd36fa469ca6c3194ceacb178be793f3ce828": (
        "missing_safeguard",
        "跨服务身份与计算路径缺少稳定协议和私网可达保障。",
    ),
    "da79c0641a4c3d6b88a6b25973fb9f2e42f2d8c8": (
        "wrong_code",
        "身份生成逻辑错误地把环境前缀写入稳定用户 ID。",
    ),
    "e23f25e14ae0c7f347dc2611a74133c5655691bf": (
        "missing_safeguard",
        "部署命令后遗漏提交、健康与配置生效验证。",
    ),
    "efb374a4c832a08e82c14ac5503a0d2416baaab5": (
        "wrong_code",
        "认证路由分类逻辑错误地把受控文档标成公开。",
    ),
    "2e87ffc1c9cb361fc0ecaadd707d3a0dbc6bfd1a": (
        "missing_safeguard",
        "模型可见 schema 遗漏了参数结构与语义约束。",
    ),
    "6349a866212bbbd890975a1a0e1fecccaeb4f9dc": (
        "missing_safeguard",
        "工具调用链遗漏了可信 viewer 身份传播。",
    ),
    "781ee3be73500cdfde29833a4b99262b693c8a3c": (
        "wrong_code",
        "流式发送与 CORS 中间件顺序均已实现但执行逻辑错误。",
    ),
    "b21dbb39746df4bf230eaa77fea3ee69d8e910a1": (
        "missing_safeguard",
        "生产身份模式缺少 fail-closed 配置守卫。",
    ),
    "fd679bef3456f77102e2854cd90a3ba2922c35f2": (
        "missing_safeguard",
        "可信身份请求缺少禁止公网 fallback 的边界守卫。",
    ),
}


def read_targets(path: Path) -> list[dict[str, Any]]:
    payload = json.loads(path.read_text())
    if not isinstance(payload, list):
        raise ValueError(f"{path}: targets root must be an array")
    return payload


def migrate_v1(path: Path) -> list[dict[str, Any]]:
    targets = deepcopy(read_targets(path))
    commits = {row["introducing_commit"] for row in targets}
    if commits != set(V1_LABELS):
        missing = sorted(commits - set(V1_LABELS))
        stale = sorted(set(V1_LABELS) - commits)
        raise ValueError(f"v1 label map mismatch: missing={missing}, stale={stale}")
    for target in targets:
        label, basis = V1_LABELS[target["introducing_commit"]]
        target["defect_class"] = label
        target["defect_class_basis"] = basis
        for item in target["defects"]:
            item["defect_class"] = label
            item["defect_class_basis"] = basis
    return targets


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--v1", required=True, type=Path)
    parser.add_argument("--run-dir", action="append", default=[], type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()

    targets = migrate_v1(args.v1)
    seen = {(row["repo"], row["introducing_commit"]) for row in targets}
    for run_dir in args.run_dir:
        for row in read_targets(run_dir / "targets.json"):
            key = (row["repo"], row["introducing_commit"])
            if key in seen:
                continue
            seen.add(key)
            targets.append(row)

    targets.sort(key=lambda row: (row["repo"], row["introducing_commit"]))
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(targets, ensure_ascii=False, indent=2) + "\n")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
