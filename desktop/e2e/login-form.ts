import type { Page } from '@playwright/test';

/** Avoid Playwright fill call logs containing a profile or password when the form is unavailable. */
export async function privateLoginForm(page: Page, address: string, password: string): Promise<void> {
  await page.evaluate(
    ({ address, password }) => {
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
      for (const [label, value] of [
        ['Адрес сервера', address],
        ['Пароль', password],
      ]) {
        const input = document.querySelector<HTMLInputElement>(`input[aria-label="${label}"]`);
        if (!setter || !input) throw new Error('Test login form unavailable.');
        setter.call(input, value);
        input.dispatchEvent(new Event('input', { bubbles: true }));
      }
    },
    { address, password },
  );
}
