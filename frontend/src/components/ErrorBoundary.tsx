import { Component, type ReactNode } from 'react';

interface State {
  error: Error | null;
}

// A crash must never be a silent white window: show the error and where to
// look. This is the last line of defence, not an excuse to throw.
export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error) {
    console.error('ui crashed:', error);
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <main className="fixed inset-0 flex items-center justify-center bg-bg-0 p-8">
        <div className="max-w-[560px] rounded-lg bg-bg-1 p-5 shadow-[var(--sh-md)]">
          <h1 className="mb-2 text-sm font-medium text-danger">Интерфейс упал</h1>
          <pre className="mb-3 overflow-x-auto whitespace-pre-wrap break-words font-mono text-xs leading-relaxed text-text-2">
            {String(this.state.error.stack || this.state.error)}
          </pre>
          <p className="text-xs text-text-3">
            Перезапустите приложение и соберите диагностику (кнопка-круг в сайдбаре). Лог: gul.log в конфиг-папке.
          </p>
        </div>
      </main>
    );
  }
}
