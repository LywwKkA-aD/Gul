import { expect, type Page } from '@playwright/test';
import type { ScreenQuality } from '../src/renderer/media/screen-settings.ts';

/** Confirm the production quality dialog; the next click retains native capture consent. */
export async function startScreen(page: Page, quality?: ScreenQuality): Promise<void> {
  await page.getByRole('button', { name: 'Показать экран', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Начать демонстрацию', exact: true });
  await expect(dialog).toBeVisible();
  if (quality)
    await dialog.getByRole('combobox', { name: 'Качество демонстрации', exact: true }).selectOption(quality);
  await dialog.getByRole('button', { name: 'Выбрать экран', exact: true }).click();
}
