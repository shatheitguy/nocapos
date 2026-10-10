import { Component, type ErrorInfo, type ReactNode } from 'react';
import { Icon } from './Icon';

interface Props {
  /** Shown in the message and the console. */
  title: string;
  /**
   * 'panel' (an app window): a message with a "Reload app" button.
   * 'hidden' (a piece of the shell): the broken piece just disappears.
   * 'screen' (the whole UI): a full-screen "Something went wrong · Reload".
   */
  fallback?: 'panel' | 'hidden' | 'screen';
  children: ReactNode;
}

/** Keeps one crashing part of the UI from taking the rest down with it. */
export class ErrorBoundary extends Component<Props, { error: Error | null }> {
  state = { error: null as Error | null };
  static getDerivedStateFromError(error: Error) {
    return { error };
  }
  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(`${this.props.title} crashed`, error, info.componentStack);
  }
  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    const kind = this.props.fallback ?? 'panel';
    if (kind === 'hidden') return null;
    if (kind === 'screen') {
      return (
        <div className="empty" style={{ position: 'fixed', inset: 0 }}>
          <Icon name="alert" size={32} />
          <h3>Something went wrong</h3>
          <p className="muted">{error.message}</p>
          <button type="button" onClick={() => location.reload()}>
            Reload
          </button>
        </div>
      );
    }
    return (
      <div className="empty">
        <Icon name="alert" size={32} />
        <h3>{this.props.title} stopped working</h3>
        <p className="muted">{error.message}</p>
        <button type="button" onClick={() => this.setState({ error: null })}>
          Reload app
        </button>
      </div>
    );
  }
}
