import { useEffect, useRef, useState } from 'react';
import { GearSixIcon } from '@phosphor-icons/react/dist/csr/GearSix';
import { MicrophoneIcon } from '@phosphor-icons/react/dist/csr/Microphone';
import { MicrophoneSlashIcon } from '@phosphor-icons/react/dist/csr/MicrophoneSlash';
import { PaperPlaneRightIcon } from '@phosphor-icons/react/dist/csr/PaperPlaneRight';
import { SpeakerHighIcon } from '@phosphor-icons/react/dist/csr/SpeakerHigh';
import { SpeakerSlashIcon } from '@phosphor-icons/react/dist/csr/SpeakerSlash';
import { XIcon } from '@phosphor-icons/react/dist/csr/X';
import {
  Avatar,
  AVATAR_TINTS,
  Badge,
  Button,
  Field,
  IconButton,
  Spinner,
  TextInput,
  Tooltip,
  VoiceStateIcon,
  type AvatarSize,
} from '../ui';

/* ─── page scaffolding ─────────────────────────────────────────────────── */

function Section({ title, note, children }: { title: string; note?: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-4">
      <div className="flex flex-col gap-1">
        <h2 className="font-display text-[15px] font-medium tracking-[.06em] uppercase">{title}</h2>
        {note && <p className="max-w-[70ch] text-sm text-text-2">{note}</p>}
      </div>
      {children}
    </section>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[140px_minmax(0,1fr)] items-center gap-4">
      <span className="font-mono text-xs text-text-3">{label}</span>
      <div className="flex min-w-0 flex-wrap items-center gap-3">{children}</div>
    </div>
  );
}

/* ─── colour swatches ──────────────────────────────────────────────────── */

const LIGHT_SCALE = ['--bg-0', '--bg-1', '--bg-2', '--bg-3', '--bg-4', '--line', '--line-soft'];
const LIGHT_TEXT = ['--text-1', '--text-2', '--text-3'];
const SIDEBAR_SCALE = ['--sb-0', '--sb-1', '--sb-2', '--sb-3', '--sb-line', '--sb-active', '--sb-danger'];
const SIDEBAR_TEXT = ['--sb-text-1', '--sb-text-2', '--sb-text-3'];
const ACCENT_SCALE = [
  '--accent',
  '--accent-hover',
  '--accent-active',
  '--accent-weak',
  '--accent-line',
  '--accent-text',
  '--on-accent',
  '--speak',
  '--speak-halo',
];
const SEMANTIC_SCALE = ['--danger', '--danger-weak', '--success', '--warning', '--warning-weak'];

/** Reads the *resolved* background of every swatch, so color-mix derivatives
    show their real value and a change of --accent is visible at a glance. */
function useResolvedColors(ref: React.RefObject<HTMLDivElement | null>) {
  const [values, setValues] = useState<Record<string, string>>({});
  useEffect(() => {
    const root = ref.current;
    if (!root) return;
    const next: Record<string, string> = {};
    root.querySelectorAll<HTMLElement>('[data-token]').forEach((node) => {
      const token = node.dataset.token;
      if (token) next[token] = getComputedStyle(node).backgroundColor;
    });
    setValues(next);
  }, [ref]);
  return values;
}

function Swatch({ token, value }: { token: string; value?: string }) {
  return (
    <div className="flex w-[128px] flex-col gap-1">
      <div
        data-token={token}
        className="h-12 w-full rounded-md shadow-[0_0_0_1px_var(--line)]"
        style={{ background: `var(${token})` }}
      />
      <span className="font-mono text-[10px] text-text-1">{token}</span>
      <span className="font-mono text-[10px] text-text-3">{value ?? '—'}</span>
    </div>
  );
}

/* ─── sections ─────────────────────────────────────────────────────────── */

function Colors() {
  const ref = useRef<HTMLDivElement>(null);
  const resolved = useResolvedColors(ref);
  const group = (tokens: string[]) => (
    <div className="flex flex-wrap gap-3">
      {tokens.map((t) => (
        <Swatch key={t} token={t} value={resolved[t]} />
      ))}
    </div>
  );

  return (
    <div ref={ref} className="flex flex-col gap-6">
      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium text-text-2">Светлая шкала</h3>
        {group(LIGHT_SCALE)}
      </div>
      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium text-text-2">Текст на светлом</h3>
        {group(LIGHT_TEXT)}
      </div>
      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium text-text-2">Тёмный сайдбар</h3>
        {group(SIDEBAR_SCALE)}
      </div>
      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium text-text-2">Текст на сайдбаре</h3>
        {group(SIDEBAR_TEXT)}
      </div>
      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium text-text-2">Акцент и речь</h3>
        {group(ACCENT_SCALE)}
      </div>
      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium text-text-2">Семантика</h3>
        {group(SEMANTIC_SCALE)}
      </div>
    </div>
  );
}

const SAMPLE = 'Съешь ещё этих мягких булок · The quick brown fox · 0123456789';

function Typography() {
  return (
    <div className="flex flex-col gap-5">
      <Row label="--font-display">
        <div className="flex flex-col gap-1">
          <span className="font-display text-[22px] font-medium tracking-[.04em]">ГУЛ · НАСТРОЙКИ · ПОДВАЛ</span>
          <span className="font-display text-xs">Martian Mono 400 · latin/cyrillic</span>
        </div>
      </Row>
      <Row label="--font-ui">
        <div className="flex flex-col gap-1">
          <span className="font-ui text-ui">{SAMPLE}</span>
          <span className="font-ui text-ui font-medium">{SAMPLE}</span>
        </div>
      </Row>
      <Row label="--font-mono">
        <div className="flex flex-col gap-1">
          <span className="font-mono text-sm">{SAMPLE}</span>
          <span className="font-mono text-sm font-medium">{SAMPLE}</span>
        </div>
      </Row>
      <Row label="--fs-ui / text-ui">
        <span className="text-ui">14px — интерфейсный кегль</span>
      </Row>
      <Row label="--fs-sm / text-sm">
        <span className="text-sm">13px — второстепенный текст</span>
      </Row>
      <Row label="--fs-xs / text-xs">
        <span className="text-xs tracking-[.08em] uppercase">11px — каптион</span>
      </Row>
    </div>
  );
}

const RADII: Array<[string, string]> = [
  ['--r-sm', 'rounded-sm'],
  ['--r-md', 'rounded-md'],
  ['--r-lg', 'rounded-lg'],
  ['--r-pill', 'rounded-pill'],
];

function Radii() {
  return (
    <div className="flex flex-wrap gap-4">
      {RADII.map(([token, cls]) => (
        <div key={token} className="flex flex-col gap-1">
          <div className={`h-16 w-24 bg-bg-3 shadow-[0_0_0_1px_var(--line)] ${cls}`} />
          <span className="font-mono text-[10px] text-text-3">
            {token} · {cls}
          </span>
        </div>
      ))}
    </div>
  );
}

const AVATAR_SIZES: AvatarSize[] = [20, 24, 30, 36];

/* One row per voice state, plus a name long enough that it has to truncate
   before the glyph after it gives up a single pixel. */
const STATE_ROWS = [
  { name: 'Говорит', tint: 1, muted: false, deaf: false },
  { name: 'Микрофон выключен', tint: 3, muted: true, deaf: false },
  { name: 'Ничего не слышит', tint: 4, muted: true, deaf: true },
  { name: 'Александра Константинопольская-Задунайская', tint: 6, muted: true, deaf: false },
] as const;

function Avatars() {
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium text-text-2">4 размера × 8 тинтов, покой</h3>
        {AVATAR_SIZES.map((size) => (
          <Row key={size} label={`size ${size}`}>
            {AVATAR_TINTS.map((_, tint) => (
              <Avatar key={tint} size={size} tint={tint} initials="ГУ" />
            ))}
          </Row>
        ))}
      </div>

      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium text-text-2">Речь — ореол gul-halo, задержка 260ms на строку</h3>
        {AVATAR_SIZES.map((size) => (
          <Row key={size} label={`speaking ${size}`}>
            {AVATAR_TINTS.map((_, tint) => (
              <Avatar key={tint} size={size} tint={tint} initials="ГУ" speaking haloIndex={tint} />
            ))}
          </Row>
        ))}
      </div>

      <div className="flex flex-col gap-3">
        <h3 className="text-sm font-medium text-text-2">
          Мьют и глухота — после имени, не на аватаре; глухота показывается одна
        </h3>
        <div className="flex w-[220px] flex-col rounded-md bg-bg-1 p-2 shadow-[var(--sh-sm)]">
          {STATE_ROWS.map((row) => (
            <div
              key={row.name}
              className="flex h-[var(--item-h)] min-w-0 items-center gap-2 rounded-md px-2 text-sm text-text-2"
            >
              <Avatar size={24} tint={row.tint} initials="ГУ" />
              <span className="min-w-0 flex-1 truncate">{row.name}</span>
              <VoiceStateIcon muted={row.muted} deaf={row.deaf} surface="light" />
            </div>
          ))}
        </div>
      </div>

      <div className="flex flex-col gap-3 rounded-md bg-sb-1 p-4">
        <h3 className="text-sm font-medium text-sb-text-2">То же на тёмной шкале сайдбара</h3>
        <div className="flex w-[220px] flex-col">
          {STATE_ROWS.map((row, i) => (
            <div
              key={row.name}
              className="flex h-[var(--item-h)] min-w-0 items-center gap-2 rounded-md px-2 text-sm text-sb-text-3"
            >
              <Avatar size={20} tint={row.tint} initials="ГУ" speaking={i === 0} haloIndex={i} />
              <span className="min-w-0 flex-1 truncate">{row.name}</span>
              <VoiceStateIcon muted={row.muted} deaf={row.deaf} surface="sidebar" />
            </div>
          ))}
        </div>
      </div>

      <Row label="photo / self">
        <Avatar size={36} tint={2} initials="ГУ" photo />
        <Avatar size={36} tint={5} initials="ГУ" photo speaking />
        <Avatar size={30} tint={0} initials="ИГ" self speaking />
      </Row>
    </div>
  );
}

function Buttons() {
  return (
    <div className="flex flex-col gap-5">
      <Row label="primary">
        <Button size="lg">Подключиться</Button>
        <Button size="md">Готово</Button>
        <Button size="md" disabled>
          Готово
        </Button>
        <Button size="lg" disabled>
          <Spinner /> Подключение…
        </Button>
      </Row>
      <Row label="quiet">
        <Button variant="quiet" size="md">
          <MicrophoneIcon size={15} /> Проверить микрофон
        </Button>
        <Button variant="quiet" size="lg">
          Отмена
        </Button>
        <Button variant="quiet" size="md" disabled>
          Проверить микрофон
        </Button>
      </Row>
      <Row label="badge">
        <Badge>1</Badge>
        <Badge>12</Badge>
        <Badge>99+</Badge>
      </Row>
      <Row label="spinner">
        <Spinner />
        <Spinner size={24} durationMs={1400} />
        <span className="text-[var(--accent-text)]">
          <Spinner size={32} />
        </span>
      </Row>
    </div>
  );
}

function IconButtons() {
  return (
    <div className="flex flex-col gap-5">
      <Row label="light">
        <IconButton>
          <GearSixIcon size={16} />
        </IconButton>
        <IconButton active>
          <GearSixIcon size={16} weight="fill" />
        </IconButton>
        <IconButton tone="danger" active>
          <MicrophoneSlashIcon size={16} weight="fill" />
        </IconButton>
        <IconButton disabled>
          <XIcon size={16} />
        </IconButton>
      </Row>
      <div className="grid grid-cols-[140px_minmax(0,1fr)] items-center gap-4 rounded-md bg-sb-1 p-4">
        <span className="font-mono text-xs text-sb-text-3">sidebar</span>
        <div className="flex flex-wrap items-center gap-3">
          <IconButton surface="sidebar">
            <MicrophoneIcon size={16} />
          </IconButton>
          <IconButton surface="sidebar" active>
            <SpeakerHighIcon size={16} weight="fill" />
          </IconButton>
          <IconButton surface="sidebar" tone="danger" active>
            <MicrophoneSlashIcon size={16} weight="fill" />
          </IconButton>
          <IconButton surface="sidebar" tone="danger" active>
            <SpeakerSlashIcon size={16} weight="fill" />
          </IconButton>
          <IconButton surface="sidebar">
            <GearSixIcon size={16} />
          </IconButton>
          <IconButton surface="sidebar" disabled>
            <GearSixIcon size={16} />
          </IconButton>
        </div>
      </div>
      <Row label="accent">
        <IconButton surface="accent" aria-label="Отправить">
          <PaperPlaneRightIcon size={15} weight="fill" />
        </IconButton>
        <IconButton surface="accent" disabled aria-label="Отправить">
          <PaperPlaneRightIcon size={15} weight="fill" />
        </IconButton>
      </Row>
    </div>
  );
}

function Tooltips() {
  // The halo is the one state that cannot be shown standing still: it has to
  // be switched off to see the phrase end fade out.
  const [speaking, setSpeaking] = useState(true);

  return (
    <div className="flex flex-col gap-5">
      <Row label="light">
        <Tooltip label="Настройки">
          <IconButton aria-label="Настройки">
            <GearSixIcon size={16} />
          </IconButton>
        </Tooltip>
        <Tooltip label="Собрать диагностику (логи и версия в zip)">
          <IconButton aria-label="Диагностика">
            <SpeakerHighIcon size={16} />
          </IconButton>
        </Tooltip>
      </Row>
      <div className="grid grid-cols-[140px_minmax(0,1fr)] items-center gap-4 rounded-md bg-sb-1 p-4">
        <span className="font-mono text-xs text-sb-text-3">sidebar</span>
        <div className="flex flex-wrap items-center gap-3">
          <Tooltip label="Выключить микрофон">
            <IconButton surface="sidebar" aria-label="Выключить микрофон">
              <MicrophoneIcon size={16} />
            </IconButton>
          </Tooltip>
          <Tooltip label="Выключить звук — не слышно никого">
            <IconButton surface="sidebar" aria-label="Выключить звук">
              <SpeakerHighIcon size={16} />
            </IconButton>
          </Tooltip>
        </div>
      </div>
      <Row label="halo">
        <Avatar size={36} tint={0} initials="ГУ" speaking={speaking} />
        <Avatar size={30} tint={2} initials="ГУ" speaking={speaking} haloIndex={1} />
        <Avatar size={24} tint={4} initials="ГУ" speaking={speaking} haloIndex={2} />
        <Button variant="quiet" onClick={() => setSpeaking((s) => !s)}>
          {speaking ? 'Замолчать' : 'Заговорить'}
        </Button>
      </Row>
    </div>
  );
}

function Fields() {
  return (
    <div className="flex w-full max-w-[420px] flex-col gap-4 rounded-lg bg-bg-1 p-5 shadow-[var(--sh-md)]">
      <Field label="Адрес сервера">
        <TextInput mono defaultValue="voice.example.test:7443" />
      </Field>
      <Field label="Ник">
        <TextInput placeholder="как тебя зовут" />
      </Field>
      <Field label="Отключённое поле">
        <TextInput mono defaultValue="voice.example.test:7443" disabled />
      </Field>
      <label className="flex items-center gap-2 text-sm text-text-2">
        <input type="checkbox" defaultChecked />
        <span>Доверять сертификату сервера</span>
      </label>
      <Button size="lg">Подключиться</Button>
    </div>
  );
}

/* ─── page ─────────────────────────────────────────────────────────────── */

export default function Styleguide() {
  return (
    // html/body are overflow:hidden (base.css) and #root has no height of its
    // own, so the showcase owns the viewport and scrolls inside itself.
    <div className="fixed inset-0 overflow-y-auto bg-bg-0 text-text-1">
      <div className="mx-auto flex max-w-[1040px] flex-col gap-10 px-8 py-10">
        <header className="flex flex-col gap-2">
          <h1 className="font-display text-[22px] font-medium tracking-[.04em]">ГУЛ · ВИТРИНА</h1>
          <p className="max-w-[70ch] text-sm text-text-2">
            Дев-экран дизайн-системы: токены из styles/tokens.css, примитивы из components/ui.
            Открывается по хэшу <span className="font-mono">#styleguide</span>. Ни одного магического
            hex вне tokens.css и AVATAR_TINTS.
          </p>
        </header>

        <Section title="Цвет" note="Две шкалы поверхностей плюс семантика. Под каждым свотчем — вычисленное значение, поэтому производные color-mix видно вживую.">
          <Colors />
        </Section>

        <Section title="Типографика" note="Три семейства, начертания 400 и 500, три кегля. Кириллица Martian Mono проверяется строками ГУЛ / НАСТРОЙКИ / ПОДВАЛ.">
          <Typography />
        </Section>

        <Section title="Радиусы">
          <Radii />
        </Section>

        <Section title="Аватары" note="Размеры 20 / 24 / 30 / 36, восемь тинтов, состояния речи и мьюта на обеих поверхностях.">
          <Avatars />
        </Section>

        <Section title="Иконочные кнопки" note="28×28, r-md; tone=danger красит только активное состояние — как мьют и глухота в подвале сайдбара.">
          <IconButtons />
        </Section>

        <Section
          title="Тултипы и речь"
          note="Тултип — тёмная плашка над контролом, появляется через 280 мс наведения и по фокусу с клавиатуры; ореол речи гаснет за --t-slow, а не пропадает."
        >
          <Tooltips />
        </Section>

        <Section title="Кнопки, бейджи, спиннер">
          <Buttons />
        </Section>

        <Section title="Поля">
          <Fields />
        </Section>
      </div>
    </div>
  );
}
