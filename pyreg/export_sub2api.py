# -*- coding: utf-8 -*-
"""把一个 grok/xai 账号导出成 sub2api 官方 ImportData JSON（可直接在 sub2api「导入数据」选中）。

输出结构与 GrokRegisterAgent 的 register/cpa_to_sub2api.py 完全一致：
  文档头: {type:"sub2api-data", version:1, exported_at, proxies:[], accounts:[...]}
  账号:  {name, platform:"grok", type:"oauth", credentials{...}, extra{...}, concurrency, priority}

核心字段 access_token / refresh_token 来自 xai OAuth mint，网页注册本身不产生。
"""
from __future__ import annotations

import json
import re
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Optional

# sub2api / xai 常量（对齐 Wei-Shaw/sub2api 与 xai 默认值）
SUB2API_PLATFORM_GROK = "grok"
SUB2API_ACCOUNT_TYPE_OAUTH = "oauth"
SUB2API_DATA_TYPE = "sub2api-data"
SUB2API_DATA_VERSION = 1
XAI_DEFAULT_CLIENT_ID = "b1a00492-073a-47ea-816f-4c329264a828"
XAI_DEFAULT_CLI_BASE_URL = "https://cli-chat-proxy.grok.com/v1"


def _now_iso() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def _email_key(email: str) -> str:
    return str(email or "").strip().lower().replace("@", "_at_").replace(".", "_")


def _parse_expires_unix(raw: Any) -> Optional[int]:
    if raw is None or raw == "":
        return None
    if isinstance(raw, (int, float)):
        ts = float(raw)
        if ts > 1e12:  # ms
            ts /= 1000.0
        return int(ts) if ts > 1e9 else None
    s = str(raw).strip()
    if not s:
        return None
    if re.fullmatch(r"\d+(\.\d+)?", s):
        return _parse_expires_unix(float(s))
    try:
        if s.endswith("Z"):
            s = s[:-1] + "+00:00"
        dt = datetime.fromisoformat(s)
        if dt.tzinfo is None:
            dt = dt.replace(tzinfo=timezone.utc)
        return int(dt.timestamp())
    except ValueError:
        return None


def _normalize_expires_at(raw: Any) -> str:
    unix = _parse_expires_unix(raw)
    if unix is not None:
        try:
            return (
                datetime.fromtimestamp(unix, tz=timezone.utc)
                .replace(microsecond=0)
                .isoformat()
                .replace("+00:00", "Z")
            )
        except (OSError, OverflowError, ValueError):
            pass
    s = str(raw or "").strip()
    if not s:
        return ""
    if re.match(r"^\d{4}-\d{2}-\d{2}T", s) and s.endswith("+00:00"):
        return s[:-6] + "Z"
    return s


def xai_to_sub2api_account(
    *,
    email: str,
    access_token: str,
    refresh_token: str,
    id_token: str = "",
    sub: str = "",
    token_type: str = "Bearer",
    client_id: str = "",
    base_url: str = "",
    expires_at: Any = None,
    model_ids: Optional[list[str]] = None,
    source: str = "nodriver_demo",
) -> dict[str, Any]:
    """单个账号 → sub2api DataAccount（对齐 validateDataAccount / BuildAccountCredentials）。"""
    email = str(email or "").strip()
    name = email or "grok-oauth"
    access = str(access_token or "").strip()
    refresh = str(refresh_token or "").strip()
    if not access:
        raise ValueError("missing access_token")
    if not refresh:
        raise ValueError("missing refresh_token")

    client_id = str(client_id or "").strip() or XAI_DEFAULT_CLIENT_ID
    base_url = (str(base_url or "").strip() or XAI_DEFAULT_CLI_BASE_URL).rstrip("/")
    if "cli-chat-proxy.grok.com" in base_url and not base_url.endswith("/v1"):
        base_url += "/v1"

    credentials: dict[str, Any] = {
        "access_token": access,
        "refresh_token": refresh,
        "token_type": token_type or "Bearer",
        "client_id": client_id,
        "base_url": base_url,
    }
    exp = _normalize_expires_at(expires_at)
    if exp:
        credentials["expires_at"] = exp
    if id_token:
        credentials["id_token"] = str(id_token).strip()
    if email:
        credentials["email"] = email
    if sub:
        credentials["sub"] = str(sub).strip()

    account: dict[str, Any] = {
        "name": name,
        "platform": SUB2API_PLATFORM_GROK,
        "type": SUB2API_ACCOUNT_TYPE_OAUTH,
        "credentials": credentials,
        "extra": {
            "auth_provider": "xai",
            "provider": "xai",
            "source": source,
            "email": email,
            "email_key": _email_key(email),
            "name": name,
            "last_refresh": _now_iso(),
        },
        "concurrency": 1,
        "priority": 0,
    }

    ids = [str(x).strip() for x in (model_ids or []) if str(x or "").strip()]
    if ids:
        account["extra"]["model_ids"] = ids
        credentials["models"] = ids
        credentials["available_models"] = ids

    # 无 refresh 才钉账号级 expires_at + auto_pause（有 refresh 不写，避免被误暂停）
    if not refresh:
        u = _parse_expires_unix(expires_at)
        if u is not None:
            account["expires_at"] = u
            account["auto_pause_on_expired"] = True

    return account


def build_sub2api_document(accounts: list[dict[str, Any]]) -> dict[str, Any]:
    """官方 Export/Import 文档（sub2api ImportDataModal 可直接选文件导入）。"""
    return {
        "type": SUB2API_DATA_TYPE,
        "version": SUB2API_DATA_VERSION,
        "exported_at": _now_iso(),
        "proxies": [],
        "accounts": accounts,
    }


def export_account(out_path: str | Path, **account_fields: Any) -> Path:
    """便捷入口：单账号 → 写 sub2api-data JSON 文件，返回路径。"""
    account = xai_to_sub2api_account(**account_fields)
    doc = build_sub2api_document([account])
    out_path = Path(out_path).expanduser().resolve()
    out_path.parent.mkdir(parents=True, exist_ok=True)
    out_path.write_text(
        json.dumps(doc, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )
    return out_path


if __name__ == "__main__":
    # 演示：字段齐了就能导出与仓库一致的文件
    p = export_account(
        "sub2api-demo.json",
        email="demo@example.com",
        access_token="ACCESS_TOKEN_HERE",
        refresh_token="REFRESH_TOKEN_HERE",
    )
    print("已导出:", p)
