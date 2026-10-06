// Helpers shared by the LLM-driven demo Agents.

import type { Page } from "playwright";

import type { ClearedCheck } from "../src/index.ts";

// Reports whether the page holds a CAPTCHA token: from the widget's API, or
// from the hidden response field reCAPTCHA and hCaptcha fill in on success.
// The script is a string: tsx would wrap a function's inner functions in a
// __name helper that does not exist in the page.
const CAPTCHA_TOKEN = `(() => {
  const fromApi = (w) => {
    try {
      return typeof w?.getResponse === "function" && w.getResponse() !== "";
    } catch {
      return false; // hCaptcha throws before it renders
    }
  };
  const fields = document.querySelectorAll('textarea[name="g-recaptcha-response"], textarea[name="h-captcha-response"]');
  return fromApi(window.grecaptcha) || fromApi(window.hcaptcha) || [...fields].some((f) => f.value !== "");
})()`;
export const hasCaptchaToken = (page: Page) => page.evaluate<boolean>(CAPTCHA_TOKEN);

// Cleared once the page holds a CAPTCHA token, or once the blocked page has
// navigated away (e.g. an interstitial that redirects after the check).
export function captchaCleared(blockedUrl: string): ClearedCheck {
  return async (page) => page.url() !== blockedUrl || hasCaptchaToken(page);
}

export const say = (who: string, text: string) => console.log(`\n${who}: ${text}`);
