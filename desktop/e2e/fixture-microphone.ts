import { expect, type Page } from '@playwright/test';

/** A generic Chromium default label is not a calibration capability. Select the actual test input.
 * Uses the normal Settings/restart flow; opaque device IDs never enter Playwright assertion logs.
 */
export async function selectFixtureMicrophone(page: Page, peer: number): Promise<void> {
  if (process.platform !== 'linux') return;
  if (peer !== 0 && peer !== 1) throw new Error('Unknown fixture microphone.');
  const label = `Gul-Test-Microphone-${peer}`;
  await page.getByRole('button', { name: 'Настройки', exact: true }).click();
  const selector = page.getByRole('combobox', { name: 'Устройство микрофона', exact: true });
  await selector.selectOption({ label });
  await expect
    .poll(() =>
      selector.evaluate(
        (input: HTMLSelectElement, label) => input.selectedOptions[0]?.label === label,
        label,
      ),
    )
    .toBe(true);
  await expect(selector).toBeEnabled();
  await page.keyboard.press('Escape');
}
