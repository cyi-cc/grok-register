# -*- coding: utf-8 -*-
"""SSO cookie -> xai OAuth2 PKCE tokens（浏览器 JS 注入，纯 page.evaluate，不走 page.goto）。

把 GrokRegisterAgent 的 register/cpa_pkce_mint.py（curl_cffi 纯 HTTP）改成了浏览器内 JS fetch，
和 grok_register.py 的 pkce_mint() 保持一致的策略：
  set sso cookie → page.goto(sign-in) → JS fetch gRPC CreateCookieSetterLink
  → JS fetch 跟随 redirect chain → JS fetch consent 页面 + 解析表单 POST(=Allow)
  → JS fetch token endpoint 换 access/refresh。

全程不再依赖 curl_cffi，所有请求走浏览器 JS 上下文，cookie / 指纹 / TLS 天然一致。
"""
from __future__ import annotations

import base64
import hashlib
import json
import re
import secrets
import struct
import sys
import time as _time
from typing import Any, Callable
from urllib.parse import parse_qs, unquote, urlencode, urlparse

from cloakbrowser import launch

CLIENT_ID = "b1a00492-073a-47ea-816f-4c329264a828"
SCOPE = "openid profile email offline_access grok-cli:access api:access conversations:read conversations:write workspaces:read workspaces:write"
ACCOUNTS_ORIGIN = "https://accounts.x.ai"
AUTH_ORIGIN = "https://auth.x.ai"
CREATE_COOKIE_SETTER_RPC = f"{ACCOUNTS_ORIGIN}/auth_mgmt.AuthManagement/CreateCookieSetterLink"
TOKEN_ENDPOINT = f"{AUTH_ORIGIN}/oauth2/token"
DEFAULT_REDIRECT_URI = "http://127.0.0.1:56121/callback"
DEFAULT_REFERRER = "grok-build"

LogFn = Callable[[str], None]


class PKCEMintError(RuntimeError):
    """PKCE 路径失败。"""


def _noop(_: str) -> None:
    return None


def _log(msg: str) -> None:
    print(msg, file=sys.stderr, flush=True)


# ==================== PKCE 工具 ====================

def _b64url(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).decode("ascii").rstrip("=")


# ==================== gRPC-web / protobuf 编解码（Python 侧） ====================

def _encode_varint(value: int) -> bytes:
    out = bytearray()
    while True:
        b = value & 0x7F
        value >>= 7
        if value:
            out.append(b | 0x80)
        else:
            out.append(b)
            return bytes(out)


def _encode_string(field_no: int, text: str) -> bytes:
    raw = text.encode("utf-8")
    return _encode_varint((field_no << 3) | 2) + _encode_varint(len(raw)) + raw


def _encode_grpc_body(success_url: str, referrer: str) -> bytes:
    msg = _encode_string(1, success_url) + _encode_string(2, referrer)
    return b"\x00" + struct.pack(">I", len(msg)) + msg


def _read_varint(data: bytes, i: int) -> tuple[int, int]:
    result = shift = 0
    while True:
        b = data[i]; i += 1
        result |= (b & 0x7F) << shift
        if not (b & 0x80):
            return result, i
        shift += 7


def _decode_message(data: bytes) -> list[dict[str, Any]]:
    fields: list[dict[str, Any]] = []
    i, n = 0, len(data)
    while i < n:
        tag, i = _read_varint(data, i)
        field_no, wt = tag >> 3, tag & 0x07
        if wt == 0:
            val, i = _read_varint(data, i)
            fields.append({"field": field_no, "type": "varint", "value": val})
        elif wt == 1:
            fields.append({"field": field_no, "type": "fixed64", "hex": data[i:i + 8].hex()})
            i += 8
        elif wt == 2:
            ln, i = _read_varint(data, i)
            chunk = data[i:i + ln]; i += ln
            try:
                s = chunk.decode("utf-8")
                if any(c.isprintable() or c in '\n\r\t' for c in s):
                    fields.append({"field": field_no, "type": "string", "value": s})
                    continue
            except UnicodeDecodeError:
                pass
            fields.append({"field": field_no, "type": "bytes", "hex": chunk.hex(), "len": ln})
        elif wt == 5:
            fields.append({"field": field_no, "type": "fixed32", "hex": data[i:i + 4].hex()})
            i += 4
        else:
            raise ValueError(f"unsupported wire type {wt}")
    return fields


def _parse_grpc_response(body: bytes) -> dict[str, Any]:
    messages: list[list[dict[str, Any]]] = []
    i, n = 0, len(body)
    while i + 5 <= n:
        flag = body[i]
        length = struct.unpack(">I", body[i + 1:i + 5])[0]
        if i + 5 + length > n:
            break
        payload = body[i + 5:i + 5 + length]; i += 5 + length
        if flag & 0x80:
            continue  # trailers
        else:
            try:
                messages.append(_decode_message(payload))
            except Exception:
                pass
    return {"messages": messages}


def _extract_urls_from_fields(fields: list[dict[str, Any]]) -> list[str]:
    urls: list[str] = []
    for f in fields:
        if f.get("type") == "string":
            v = str(f.get("value") or "")
            if v.startswith(("http://", "https://")):
                urls.append(v)
        elif f.get("type") == "bytes" and f.get("hex"):
            try:
                urls.extend(_extract_urls_from_fields(_decode_message(bytes.fromhex(f["hex"]))))
            except Exception:
                pass
    return urls


def _extract_urls_from_bytes(data: bytes) -> list[str]:
    urls = re.findall(rb"https?://[^\s\"<>\x00-\x08\x0b\x0c\x0e-\x1f]+", data)
    return [u.decode("utf-8", errors="replace") for u in urls if u.startswith(b"http")]


def _pick_cookie_setter(urls: list[str]) -> str:
    _log(f"[pkce] all URLs ({len(urls)}): {urls[:10]}")
    for u in urls:
        if "accounts.x.ai" in u and ("set-cookie" in u or "cookie" in u.lower()):
            return u
    for u in urls:
        if "accounts.x.ai" in u and not any(x in u for x in ("/static/", "/_next/", ".js", ".css", ".png", ".woff")):
            return u
    return urls[0] if urls else ""


# ==================== 浏览器 JS 注入工具 ====================

def _grpc_fetch(page, grpc_body: bytes) -> bytes:
    """用 JS fetch 发 gRPC-web（同源，走浏览器代理，无 CORS），返回原始响应字节。"""
    b64 = base64.b64encode(grpc_body).decode()
    r = page.evaluate(f"""
        (async () => {{
            const bin = atob({json.dumps(b64)});
            const body = new Uint8Array(bin.length);
            for (let i = 0; i < bin.length; i++) body[i] = bin.charCodeAt(i);
            const resp = await fetch(
                {json.dumps(CREATE_COOKIE_SETTER_RPC)},
                {{
                    method: 'POST',
                    headers: {{
                        'content-type': 'application/grpc-web+proto',
                        'x-grpc-web': '1',
                        'x-user-agent': 'connect-es/2.1.1',
                        'origin': {json.dumps(ACCOUNTS_ORIGIN)},
                        'referer': {json.dumps(ACCOUNTS_ORIGIN)} + '/sign-in?redirect=oauth2-provider',
                    }},
                    body: body,
                    credentials: 'include',
                }}
            );
            const buf = await resp.arrayBuffer();
            const arr = new Uint8Array(buf);
            let s = '';
            const chunk = 0x8000;
            for (let i = 0; i < arr.length; i += chunk)
                s += String.fromCharCode.apply(null, arr.subarray(i, i + chunk));
            return btoa(s);
        }})()
    """)
    return base64.b64decode(r)


def _js_pkce_flow(page, state: str, cookie_setter: str, redirect_uri: str) -> dict:
    """在浏览器 JS 上下文里跑 redirect chain + consent 处理，全部 fetch，不导航页面。

    返回 {"code": str | None, "errors": [...]}
    """
    return page.evaluate(f"""
        (async () => {{
            const STATE = {json.dumps(state)};
            const REDIRECT_URI = {json.dumps(redirect_uri)};
            const ACCOUNTS_ORIGIN = {json.dumps(ACCOUNTS_ORIGIN)};
            let current = {json.dumps(cookie_setter)};
            let code = null;
            let errors = [];

            /** 从 HTML 里提取 consent 表单并 POST 提交，返回 code 或 null */
            async function tryConsentPost(pageUrl, html) {{
                // 匹配 <form method="POST" action="..."> ... </form>
                let fm = html.match(/<form\\b[^>]*method="POST"[^>]*action="([^"]+)"[^>]*>(.*?)<\\/form>/is);
                if (!fm) {{
                    fm = html.match(/<form\\b[^>]*action="([^"]+)"[^>]*method="POST"[^>]*>(.*?)<\\/form>/is);
                }}
                if (!fm) return null;

                const actionUrl = new URL(fm[1], pageUrl).href;
                const formInner = fm[2];
                const fields = {{}};

                // 提取所有 <input name="..." value="...">
                const inputRe = /<input\\b[^>]*name="([^"]*)"[^>]*value="([^"]*)"[^>]*\\/?>/gi;
                let m;
                while ((m = inputRe.exec(formInner)) !== null) {{
                    fields[m[1]] = m[2] || '';
                }}

                if (Object.keys(fields).length === 0) return null;

                const postResp = await fetch(actionUrl, {{
                    method: 'POST',
                    headers: {{
                        'Content-Type': 'application/x-www-form-urlencoded',
                        'Origin': ACCOUNTS_ORIGIN,
                        'Referer': pageUrl,
                    }},
                    body: new URLSearchParams(fields),
                    credentials: 'include',
                    redirect: 'manual',
                }});

                const loc = postResp.headers.get('location') || '';
                if (loc && loc.includes('code=')) {{
                    const abs = new URL(loc, actionUrl).href;
                    const q = new URL(abs).searchParams;
                    if (q.get('state') === STATE && q.get('code')) {{
                        return q.get('code');
                    }}
                }}

                // fallback: body 里找 code
                const body = await postResp.text();
                let cm = body.match(/"code"\\s*:\\s*"([^"]+)"/);
                if (cm) return cm[1];
                cm = body.match(/code=([A-Za-z0-9._~\\-]+)/);
                if (cm && !body.includes('error')) return cm[1];

                if (loc && loc.includes('access_denied')) {{
                    errors.push('consent_access_denied');
                    return null;
                }}
                return null;
            }}

            // ---- redirect chain ----
            for (let i = 0; i < 10 && !code; i++) {{
                try {{
                    const resp = await fetch(current, {{credentials: 'include', redirect: 'manual'}});
                    const loc = resp.headers.get('location') || '';

                    if (loc) {{
                        const abs = new URL(loc, current).href;

                        if (abs.includes('access_denied')) {{
                            errors.push('access_denied'); break;
                        }}

                        // 127.0.0.1 回调 → code
                        if (abs.includes('127.0.0.1') && abs.includes('code=')) {{
                            const q = new URL(abs).searchParams;
                            if (q.get('state') === STATE && q.get('code')) {{
                                code = q.get('code'); break;
                            }}
                        }}

                        // consent 页面 → fetch 获取 HTML 然后 POST
                        if (abs.includes('/oauth2/consent')) {{
                            const consentResp = await fetch(abs, {{credentials: 'include'}});
                            const html = await consentResp.text();
                            code = await tryConsentPost(abs, html);
                            break;
                        }}

                        current = abs;
                    }} else {{
                        // 无 redirect — 检查 body
                        const html = await resp.text();
                        if (html.includes('access_denied')) {{
                            errors.push('access_denied'); break;
                        }}
                        code = await tryConsentPost(current, html);
                        break;
                    }}
                }} catch(e) {{
                    errors.push('fetch: ' + String(e).substring(0,100));
                    break;
                }}
            }}

            return {{code: code || null, errors}};
        }})()
    """)


def _js_token_exchange(page, code: str, verifier: str, redirect_uri: str, client_id: str) -> dict:
    """在浏览器 JS 上下文里 POST token endpoint，返回 {status, body}。"""
    return page.evaluate(f"""
        (async () => {{
            const resp = await fetch({json.dumps(TOKEN_ENDPOINT)}, {{
                method: 'POST',
                headers: {{'Content-Type': 'application/x-www-form-urlencoded'}},
                body: new URLSearchParams({{
                    grant_type: 'authorization_code',
                    client_id: {json.dumps(client_id)},
                    code: {json.dumps(code)},
                    redirect_uri: {json.dumps(redirect_uri)},
                    code_verifier: {json.dumps(verifier)},
                }}),
            }});
            return {{ status: resp.status, body: await resp.text() }};
        }})()
    """)


# ==================== 主入口 ====================

def mint_with_sso_pkce(
    *,
    sso_cookie: str,
    email: str = "",
    proxy: str | None = None,
    headless: bool = True,
    timeout: float = 30.0,
    log: LogFn | None = None,
    client_id: str = CLIENT_ID,
    scope: str = SCOPE,
    redirect_uri: str = DEFAULT_REDIRECT_URI,
) -> dict[str, Any]:
    """用已有 SSO cookie 换 OAuth authorization-code token（全部 JS 注入，不导航页面）。

    参数:
        sso_cookie: 从注册阶段拿到的 SSO cookie 值
        email: 关联邮箱（仅日志）
        proxy: 代理地址
        headless: 是否无头模式
        timeout: 保留参数（浏览器内操作有自己的超时）
        log: 日志回调
        client_id / scope / redirect_uri: OAuth 参数
    """
    log = log or _noop
    sso_cookie = (sso_cookie or "").strip()
    if not sso_cookie:
        raise PKCEMintError("empty sso cookie")

    # ---- 生成 PKCE 参数 ----
    verifier = _b64url(secrets.token_bytes(48))
    challenge = _b64url(hashlib.sha256(verifier.encode()).digest())
    state = secrets.token_hex(16)
    nonce = secrets.token_hex(16)

    consent_url = f"{ACCOUNTS_ORIGIN}/oauth2/consent?" + urlencode({
        "response_type": "code", "client_id": client_id, "redirect_uri": redirect_uri,
        "scope": scope, "state": state, "code_challenge": challenge,
        "code_challenge_method": "S256", "nonce": nonce, "referrer": DEFAULT_REFERRER,
    })

    grpc_body = _encode_grpc_body(consent_url, f"{ACCOUNTS_ORIGIN}/sign-in")

    # ---- 启动浏览器 ----
    launch_kwargs: dict[str, Any] = {"headless": headless, "humanize": True}
    resolved_proxy = (proxy or "").strip() or None
    if resolved_proxy:
        launch_kwargs["proxy"] = resolved_proxy

    log(f"[pkce] browser headless={headless} proxy={'set' if resolved_proxy else 'none'}")
    browser = launch(**launch_kwargs)
    page = browser.new_page()

    try:
        # ---- 直接注入 SSO cookie（CDP 层设置，无需导航） ----
        log("[pkce] setting sso cookie (no navigation)")
        for domain in ("accounts.x.ai", ".accounts.x.ai", ".x.ai", "auth.x.ai"):
            for name in ("sso", "sso-rw"):
                try:
                    page.context.add_cookies([{
                        "name": name,
                        "value": sso_cookie,
                        "domain": domain,
                        "path": "/",
                    }])
                except Exception:
                    pass

        # ---- Step 1: gRPC CreateCookieSetterLink ----
        log("[pkce] Step 1: gRPC (JS fetch)")
        grpc_raw = _grpc_fetch(page, grpc_body)
        log(f"[pkce] gRPC raw len={len(grpc_raw)}")

        parsed = _parse_grpc_response(grpc_raw)
        fields = parsed["messages"][0] if parsed.get("messages") else []
        urls = _extract_urls_from_fields(fields) or _extract_urls_from_bytes(grpc_raw)
        cookie_setter = _pick_cookie_setter(urls)
        if not cookie_setter:
            raise PKCEMintError("gRPC 响应无有效 URL")
        log(f"[pkce] cookie-setter: {cookie_setter[:100]}")

        # ---- Step 2+3: JS 内 redirect chain + consent（全程不导航） ----
        log("[pkce] Step 2+3: redirect chain + consent (all JS fetch)")
        result = _js_pkce_flow(page, state, cookie_setter, redirect_uri)
        code = result.get("code")
        errors = result.get("errors", [])

        if not code:
            raise PKCEMintError(
                "PKCE: 无 authorization code" +
                (f" [{'; '.join(errors)}]" if errors else "")
            )
        log(f"[pkce] authorization code ok{f' email={email}' if email else ''}")

        # ---- Step 4: token exchange（JS fetch） ----
        log("[pkce] Step 4: token exchange (JS fetch)")
        tok_r = _js_token_exchange(page, code, verifier, redirect_uri, client_id)
        if tok_r["status"] != 200:
            raise PKCEMintError(f"token HTTP {tok_r['status']}: {tok_r['body'][:300]}")
        token = json.loads(tok_r["body"])
        if not token.get("access_token"):
            raise PKCEMintError("token missing access_token")

        if "expires_in" in token and "expires_at" not in token:
            token["expires_at"] = int(_time.time()) + int(token["expires_in"])

        log(f"[pkce] OK access_token len={len(token['access_token'])}")

        return {
            "access_token": str(token["access_token"]).strip(),
            "refresh_token": str(token["refresh_token"]).strip(),
            "id_token": str(token.get("id_token") or "").strip(),
            "token_type": str(token.get("token_type") or "Bearer"),
            "expires_in": int(token.get("expires_in") or 21600),
            "expires_at": token.get("expires_at"),
            "mint_channel": "pkce",
        }

    finally:
        try:
            page.close()
        except Exception:
            pass
        try:
            browser.close()
        except Exception:
            pass
