# grok_register.py — CloakBrowser 注册，取到 sso cookie 即完成
import json
import os
import sys
import time as _time
from pathlib import Path

from cloakbrowser import launch


def log(msg: str) -> None:
    print(msg, file=sys.stderr, flush=True)


def emit_result(**fields) -> None:
    print("__RESULT__" + json.dumps(fields, ensure_ascii=False), flush=True)


def emit_shot(path: str) -> None:
    print("__SHOT__" + path, flush=True)


def request_code() -> str:
    print("__NEED_CODE__", flush=True)
    return (sys.stdin.readline() or "").strip()


def read_env() -> dict:
    return {
        "email": os.environ.get("GROK_EMAIL", "").strip(),
        "password": os.environ.get("GROK_PASSWORD", "").strip(),
        "given": os.environ.get("GROK_GIVEN", "").strip(),
        "family": os.environ.get("GROK_FAMILY", "").strip(),
        "headless": os.environ.get("GROK_HEADLESS", "0") == "1",
        "proxy": os.environ.get("GROK_PROXY", "").strip() or None,
    }


def wait_turnstile(page, timeout_s=90) -> bool:
    try:
        frame = page.frame_locator("iframe[src*='turnstile']").first
        try: frame.locator(".challenge-container .mark").click(timeout=3000)
        except: pass
    except: pass
    try:
        page.wait_for_function(
            """() => {
                const el = document.querySelector('input[name="cf-turnstile-response"]');
                return el && el.value && el.value.length > 0;
            }""", timeout=timeout_s * 1000)
        return True
    except: return False


def wait_sso(page, timeout_s=120) -> str:
    deadline = _time.time() + timeout_s
    while _time.time() < deadline:
        for c in page.context.cookies():
            if c["name"] in ("sso", "sso-rw") and c["value"]:
                return c["value"]
        _time.sleep(0.5)
    return ""


# ==================== 主流程 ====================

def main() -> None:
    cfg = read_env()
    if not cfg["email"]:
        emit_result(ok=False, error="缺少 GROK_EMAIL 环境变量")
        return

    proxy = cfg["proxy"]
    log(f"启动 CloakBrowser headless={cfg['headless']} proxy={'set' if proxy else 'none'}")

    launch_kwargs = {"headless": cfg["headless"], "humanize": True}
    if proxy:
        launch_kwargs["proxy"] = proxy

    browser = launch(**launch_kwargs)

    try:
        page = browser.new_page()
        page.goto("https://accounts.x.ai/sign-up")
        log(f"已打开: {page.evaluate('document.title')}")

        page.wait_for_selector("button:has(svg.lucide-mail)", timeout=15000)
        page.click("button:has(svg.lucide-mail)")

        page.wait_for_selector("input[data-testid=email]", timeout=15000)
        page.fill("input[data-testid=email]", cfg["email"])
        page.wait_for_selector("button[type=submit]", timeout=15000)
        page.click("button[type=submit]")
        log(f"邮箱已提交")

        code = request_code()
        if not code:
            emit_result(ok=False, error="未收到验证码")
            return

        page.wait_for_selector("input[name=code]", timeout=120000)
        page.fill("input[name=code]", code)
        page.wait_for_selector("button[type=submit]", timeout=15000)
        page.click("button[type=submit]")
        log("验证码已提交")

        page.wait_for_selector("input[data-testid=givenName]", timeout=120000)
        page.fill("input[data-testid=givenName]", cfg["given"])
        page.wait_for_selector("input[data-testid=familyName]", timeout=15000)
        page.fill("input[data-testid=familyName]", cfg["family"])
        page.wait_for_selector("input[data-testid=password]", timeout=15000)
        page.fill("input[data-testid=password]", cfg["password"])
        log("资料已填写")

        if not wait_turnstile(page):
            shot = str(Path("register_fail.png").resolve())
            page.screenshot(path=shot)
            emit_shot(shot)
            emit_result(ok=False, error="Turnstile 超时")
            return
        log("Turnstile OK，提交")

        page.wait_for_selector("button[type=submit]", timeout=15000)
        page.click("button[type=submit]")
        log("注册已提交")

        sso = wait_sso(page)
        if not sso:
            emit_result(ok=False, error="SSO cookie 超时")
            return
        log(f"SSO OK len={len(sso)}")

    except Exception as e:
        try:
            shot = str(Path("register_fail.png").resolve())
            for ctx in browser.contexts:
                try:
                    if ctx.pages: ctx.pages[0].screenshot(path=shot); emit_shot(shot); break
                except: pass
        except: pass
        emit_result(ok=False, error=str(e))
        return
    finally:
        browser.close()

    emit_result(ok=True, email=cfg["email"], sso=sso)


if __name__ == "__main__":
    main()
