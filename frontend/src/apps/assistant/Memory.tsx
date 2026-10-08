import { useEffect, useState } from 'react';
import { aiApi, type Memory as Mem } from '../../api/ai';
import { Icon } from '../../components/Icon';
import { toast } from '../../state/toasts';

/** Long-term memory: facts the assistant keeps across every conversation. */
export function Memory() {
  const [enabled, setEnabled] = useState(true);
  const [items, setItems] = useState<Mem[]>([]);
  const [draft, setDraft] = useState('');
  const [editing, setEditing] = useState<{ id: string; text: string } | null>(null);

  const load = async () => {
    const r = await aiApi.memory();
    if (r.ok) {
      setEnabled(r.data.enabled);
      setItems(r.data.memories ?? []);
    }
  };
  useEffect(() => {
    void load();
  }, []);

  const toggle = async (on: boolean) => {
    setEnabled(on);
    const r = await aiApi.setMemoryEnabled(on);
    if (!r.ok) {
      setEnabled(!on);
      toast('error', 'Could not change memory setting', r.error);
    }
  };

  const add = async () => {
    const text = draft.trim();
    if (!text) return;
    const r = await aiApi.addMemory(text);
    if (r.ok) {
      setDraft('');
      void load();
    } else toast('error', 'Could not save', r.error);
  };

  const saveEdit = async () => {
    if (!editing) return;
    const r = await aiApi.updateMemory(editing.id, editing.text.trim());
    setEditing(null);
    if (r.ok) void load();
    else toast('error', 'Could not update', r.error);
  };

  const remove = async (id: string) => {
    const r = await aiApi.deleteMemory(id);
    if (r.ok) void load();
  };

  return (
    <div className="ai-panel">
      <div className="panel">
        <label className="toggle row">
          <span>
            <b>Memory</b>
            <div className="muted small">The assistant remembers these facts in every conversation.</div>
          </span>
          <input type="checkbox" checked={enabled} onChange={(e) => void toggle(e.target.checked)} />
        </label>
      </div>

      <div className="panel">
        <div className="mem-add">
          <input
            placeholder="Add something to remember, e.g. “My main server is 192.168.1.20”"
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => e.key === 'Enter' && void add()}
          />
          <button type="button" disabled={!draft.trim()} onClick={() => void add()}>
            <Icon name="plus" size={15} /> Add
          </button>
        </div>
        {items.length === 0 ? (
          <p className="muted small">Nothing saved yet. You can also tell the assistant “remember that…”.</p>
        ) : (
          <ul className="mem-list">
            {items.map((m) => (
              <li key={m.id}>
                {editing?.id === m.id ? (
                  <>
                    <input
                      value={editing.text}
                      autoFocus
                      onChange={(e) => setEditing({ id: m.id, text: e.target.value })}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') void saveEdit();
                        if (e.key === 'Escape') setEditing(null);
                      }}
                    />
                    <button type="button" className="ghost icon-btn" onClick={() => void saveEdit()} title="Save">
                      <Icon name="check" size={15} />
                    </button>
                  </>
                ) : (
                  <>
                    <span>{m.content}</span>
                    <button type="button" className="ghost icon-btn" onClick={() => setEditing({ id: m.id, text: m.content })} title="Edit">
                      <Icon name="pencil" size={14} />
                    </button>
                    <button type="button" className="ghost icon-btn danger" onClick={() => void remove(m.id)} title="Delete">
                      <Icon name="trash" size={14} />
                    </button>
                  </>
                )}
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
