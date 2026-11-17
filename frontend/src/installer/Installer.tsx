import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react";
import { useT, type T } from "../i18n";
import { Icon, type IconName } from "../components/Icon";
import { Logo } from "../components/Logo";
import { usePrefs } from "../state/prefs";
import { allZones, KEYBOARDS, REGIONS } from "./regions";
import "./installer.css";

// The NoCapOS installer: a full-screen setup on the USB stick's own screen
// (served by cmd/nocap-installer, loopback only). Six steps, no jargon:
// language & region, where NoCapOS goes, your account, how you'll use it,
// review, installing; then "Restart now".

type Kind = "nvme" | "ssd" | "hdd" | "usb" | "sd";
type SegKind = "startup" | "system" | "data" | "free";
interface Segment {
  kind: SegKind;
  bytes: number;
  label?: string;
}
interface Disk {
  path: string;
  name: string;
  model: string;
  bytes: number;
  kind: Kind;
  segments: Segment[];
  boot: boolean;
  too_small: boolean;
}
interface Info {
  version: string;
  disks: Disk[];
  min_bytes: number;
  layout: { startup: number; system: number };
  tpm: boolean;
}
interface Progress {
  running: boolean;
  step: string;
  percent: number;
  error?: string;
  done: boolean;
  recovery_key?: string;
}
type Usage = "desktop-web" | "desktop" | "web";

interface Draft {
  region: string;
  zone: string;
  keyboard: string;
  disk: string;
  encrypt: boolean;
  useTPM: boolean;
  pass: string;
  pass2: string;
  fullName: string;
  username: string;
  usernameTouched: boolean;
  password: string;
  password2: string;
  hostname: string;
  hostnameTouched: boolean;
  usage: Usage;
}

const STEPS = [
  "language",
  "disk",
  "account",
  "usage",
  "review",
  "install",
] as const;
type StepId = (typeof STEPS)[number];

async function call<R>(
  path: string,
  body?: unknown,
): Promise<{ ok: boolean; data: R; error?: string }> {
  try {
    const res = await fetch(
      path,
      body === undefined
        ? {}
        : {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          },
    );
    const data = (await res.json().catch(() => ({}))) as R & {
      error?: { message?: string };
    };
    return {
      ok: res.ok,
      data,
      error: res.ok
        ? undefined
        : (data?.error?.message ?? `HTTP ${res.status}`),
    };
  } catch (e) {
    return { ok: false, data: {} as R, error: String(e) };
  }
}

/** 500107862016 → "500 GB" (drives are sold in decimal sizes). */
export function size(bytes: number): string {
  if (bytes >= 1e12)
    return `${(bytes / 1e12).toFixed(bytes >= 1e13 ? 0 : 1).replace(/\.0$/, "")} TB`;
  if (bytes >= 1e9) return `${Math.round(bytes / 1e9)} GB`;
  return `${Math.max(1, Math.round(bytes / 1e6))} MB`;
}

const KIND_ICON: Record<Kind, IconName> = {
  nvme: "nvme",
  ssd: "ssd",
  hdd: "hdd",
  usb: "usb",
  sd: "drive",
};

/** 0-4: how hard a password is to guess, roughly. */
export function strength(p: string): number {
  if (!p) return 0;
  let s = 0;
  if (p.length >= 8) s++;
  if (p.length >= 12) s++;
  if (/[a-z]/.test(p) && /[A-Z]/.test(p)) s++;
  if (/\d/.test(p) && /[^A-Za-z0-9]/.test(p)) s++;
  if (p.length >= 16) s = Math.max(s, 3);
  if (/^(.)\1+$/.test(p) || /^(password|12345678|qwerty)/i.test(p)) s = 1;
  return Math.min(4, s);
}

const toUsername = (full: string) =>
  full
    .trim()
    .toLowerCase()
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .split(/\s+/)[0]
    ?.replace(/[^a-z0-9]/g, "")
    .slice(0, 24) ?? "";

const toHostname = (user: string) =>
  (user ? `${user}-nocap` : "nocap").slice(0, 40);

const USER_RE = /^[a-z][a-z0-9_-]{0,30}$/;
const RESERVED = new Set([
  "root",
  "admin",
  "daemon",
  "bin",
  "sys",
  "nobody",
  "nocapos",
  "docker",
  "sudo",
]);

export function Installer() {
  const t = useT();
  const [info, setInfo] = useState<Info | null>(null);
  const [loadErr, setLoadErr] = useState("");
  const [step, setStep] = useState(0);
  const [dir, setDir] = useState<1 | -1>(1);
  const [prog, setProg] = useState<Progress | null>(null);
  const [startErr, setStartErr] = useState("");
  const [understood, setUnderstood] = useState(false);
  const guessRegion =
    REGIONS.find(
      (r) => r.zone === Intl.DateTimeFormat().resolvedOptions().timeZone,
    ) ?? REGIONS[0];
  const [d, setD] = useState<Draft>({
    region: guessRegion.id,
    zone: guessRegion.zone,
    keyboard: guessRegion.keyboard,
    disk: "",
    encrypt: false,
    useTPM: true,
    pass: "",
    pass2: "",
    fullName: "",
    username: "",
    usernameTouched: false,
    password: "",
    password2: "",
    hostname: "nocap",
    hostnameTouched: false,
    usage: "desktop-web",
  });
  const set = (p: Partial<Draft>) => setD((x) => ({ ...x, ...p }));

  const load = async () => {
    const r = await call<Info>("/api/installer/info");
    if (r.ok) {
      setInfo(r.data);
      setLoadErr("");
    } else setLoadErr(r.error ?? "");
  };
  useEffect(() => {
    void load();
  }, []);

  const id: StepId = STEPS[step];
  const disk = info?.disks.find((x) => x.path === d.disk);
  const tpm = !!info?.tpm;

  const valid: Record<StepId, boolean> = {
    language: !!d.zone && !!d.keyboard,
    disk:
      !!disk &&
      !disk.boot &&
      !disk.too_small &&
      (!d.encrypt ||
        (tpm && d.useTPM) ||
        (d.pass.length >= 8 && d.pass === d.pass2)),
    account:
      d.fullName.trim().length > 0 &&
      USER_RE.test(d.username) &&
      !RESERVED.has(d.username) &&
      d.password.length >= 8 &&
      d.password === d.password2 &&
      /^[a-z0-9-]{1,40}$/.test(d.hostname),
    usage: true,
    review: understood,
    install: false,
  };

  const go = (n: number) => {
    setDir(n > step ? 1 : -1);
    setStep(n);
  };
  const next = () => {
    if (!valid[id]) return;
    if (id === "review") void start();
    else go(step + 1);
  };
  const back = () => step > 0 && id !== "install" && go(step - 1);

  const start = async () => {
    setStartErr("");
    const r = await call<Progress>("/api/installer/install", {
      disk: d.disk,
      encrypt: d.encrypt,
      use_tpm: d.encrypt && tpm && d.useTPM,
      passphrase: d.encrypt ? d.pass : "",
      account: {
        full_name: d.fullName.trim(),
        username: d.username,
        password: d.password,
      },
      hostname: d.hostname,
      timezone: d.zone,
      keyboard: d.keyboard,
      language: "en",
      usage: d.usage,
    });
    if (!r.ok) {
      setStartErr(r.error ?? "");
      return;
    }
    setProg(r.data);
    go(STEPS.indexOf("install"));
  };

  // Follow the install.
  useEffect(() => {
    if (id !== "install") return;
    const iv = window.setInterval(async () => {
      const r = await call<Progress>("/api/installer/progress");
      if (r.ok) setProg(r.data);
    }, 1000);
    return () => window.clearInterval(iv);
  }, [id]);

  // Keyboard: Enter goes on, Escape goes back.
  const nextRef = useRef(next);
  nextRef.current = next;
  const backRef = useRef(back);
  backRef.current = back;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement)?.tagName;
      if (
        e.key === "Enter" &&
        tag !== "BUTTON" &&
        tag !== "SELECT" &&
        !(e.target as HTMLElement)?.closest?.('[role="radiogroup"]')
      ) {
        e.preventDefault();
        nextRef.current();
      } else if (e.key === "Escape") backRef.current();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const pct = id === "install" ? 100 : (step / (STEPS.length - 1)) * 100;

  return (
    <div className="nci">
      <div className="nci-bar" aria-hidden>
        <div className="nci-bar-fill" style={{ width: `${pct}%` }} />
      </div>
      <header className="nci-head">
        <Logo size={34} />
        <span className="nci-stepcount">
          {id !== "install" &&
            t("in.stepOf", { n: step + 1, total: STEPS.length - 1 })}
        </span>
        <VoiceSwitch />
      </header>
      <main className={`nci-main dir-${dir === 1 ? "fwd" : "back"}`} key={id}>
        {loadErr && !info ? (
          <Card icon="alert" title={t("in.loadFail")} text={loadErr}>
            <button
              type="button"
              className="nci-btn"
              onClick={() => void load()}
            >
              {t("in.tryAgain")}
            </button>
          </Card>
        ) : !info ? (
          <div className="nci-loading">
            <span className="spinner" />
          </div>
        ) : id === "language" ? (
          <LanguageStep d={d} set={set} t={t} />
        ) : id === "disk" ? (
          <DiskStep d={d} set={set} t={t} info={info} reload={load} />
        ) : id === "account" ? (
          <AccountStep d={d} set={set} t={t} />
        ) : id === "usage" ? (
          <UsageStep d={d} set={set} t={t} />
        ) : id === "review" ? (
          <ReviewStep
            d={d}
            t={t}
            disk={disk!}
            tpm={tpm}
            understood={understood}
            setUnderstood={setUnderstood}
            err={startErr}
            onEdit={(s) => go(STEPS.indexOf(s))}
          />
        ) : (
          <InstallStep prog={prog} t={t} />
        )}
      </main>
      {id !== "install" && info && (
        <footer className="nci-foot">
          <button
            type="button"
            className="nci-btn ghost"
            disabled={step === 0}
            onClick={back}
          >
            <Icon name="chevronLeft" size={18} /> {t("in.back")}
          </button>
          <button
            type="button"
            className={`nci-btn ${id === "review" ? "danger" : "primary"}`}
            disabled={!valid[id]}
            onClick={next}
          >
            {id === "review" ? t("in.eraseInstall") : t("in.next")}{" "}
            <Icon
              name={id === "review" ? "download" : "chevronRight"}
              size={18}
            />
          </button>
        </footer>
      )}
    </div>
  );
}

function VoiceSwitch() {
  const lingo = usePrefs((s) => s.lingo);
  const set = usePrefs((s) => s.set);
  return (
    <button
      type="button"
      className="nci-voice"
      onClick={() => set({ lingo: lingo === "genz" ? "standard" : "genz" })}
      title="Gen Z / Standard"
    >
      {lingo === "genz" ? "Gen Z" : "Standard"}
    </button>
  );
}

function Card({
  icon,
  title,
  text,
  children,
  wide,
}: {
  icon: IconName;
  title: string;
  text?: string;
  children?: ReactNode;
  wide?: boolean;
}) {
  return (
    <section className={`nci-step ${wide ? "wide" : ""}`}>
      <div className="nci-step-icon">
        <Icon name={icon} size={30} />
      </div>
      <h1>{title}</h1>
      {text && <p className="nci-lead">{text}</p>}
      {children}
    </section>
  );
}

type StepProps = { d: Draft; set: (p: Partial<Draft>) => void; t: T };

// ---- 1. Language & region --------------------------------------------------------

function LanguageStep({ d, set, t }: StepProps) {
  const zones = useMemo(allZones, []);
  return (
    <Card icon="globe" title={t("in.langTitle")} text={t("in.langText")}>
      <div className="nci-glass nci-form">
        <label className="nci-field">
          <span>{t("in.language")}</span>
          <select value="en" disabled>
            <option value="en">English</option>
          </select>
        </label>
        <label className="nci-field">
          <span>{t("in.region")}</span>
          <select
            value={d.region}
            autoFocus
            onChange={(e) => {
              const r = REGIONS.find((x) => x.id === e.target.value)!;
              set({ region: r.id, zone: r.zone, keyboard: r.keyboard });
            }}
          >
            {[...REGIONS]
              .sort((a, b) => a.name.localeCompare(b.name))
              .map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
          </select>
        </label>
        <label className="nci-field">
          <span>{t("in.timezone")}</span>
          <select
            value={d.zone}
            onChange={(e) => set({ zone: e.target.value })}
          >
            {zones.map((z) => (
              <option key={z} value={z}>
                {z.replace(/_/g, " ")}
              </option>
            ))}
          </select>
        </label>
        <label className="nci-field">
          <span>{t("in.keyboard")}</span>
          <select
            value={d.keyboard}
            onChange={(e) => set({ keyboard: e.target.value })}
          >
            {KEYBOARDS.map((k) => (
              <option key={k.id} value={k.id}>
                {k.name}
              </option>
            ))}
          </select>
        </label>
        <input
          className="nci-try"
          placeholder={t("in.tryKeyboard")}
          aria-label={t("in.tryKeyboard")}
        />
      </div>
    </Card>
  );
}

// ---- 2. Where should NoCapOS go? ---------------------------------------------------

function segLabel(t: T, s: Segment) {
  const n = size(s.bytes);
  switch (s.kind) {
    case "startup":
      return t("in.segStartup", { size: n });
    case "system":
      return t("in.segSystem", { size: n });
    case "free":
      return t("in.segFree", { size: n });
    default:
      return s.label
        ? t("in.segDataNamed", { name: s.label, size: n })
        : t("in.segData", { size: n });
  }
}

function Bar({
  segments,
  total,
  labels,
  t,
}: {
  segments: { kind: string; bytes: number; text: string }[];
  total: number;
  labels?: boolean;
  t: T;
}) {
  return (
    <div className="nci-usage">
      <div
        className="nci-usage-bar"
        role="img"
        aria-label={segments.map((s) => s.text).join(", ")}
      >
        {segments.map((s, i) => (
          <span
            key={i}
            className={`nci-seg ${s.kind}`}
            style={
              {
                "--w": `${Math.max(0.8, (s.bytes / total) * 100)}%`,
                "--i": i,
              } as CSSProperties
            }
            title={s.text}
          />
        ))}
      </div>
      {labels && (
        <div className="nci-usage-legend">
          {segments
            .filter((s) => s.kind !== "startup" || segments.length === 1)
            .map((s, i) => (
              <span key={i} className={`nci-legend ${s.kind}`}>
                <i />
                {s.text}
              </span>
            ))}
          {segments.length === 0 && (
            <span className="nci-legend free">{t("in.empty")}</span>
          )}
        </div>
      )}
    </div>
  );
}

function DiskStep({
  d,
  set,
  t,
  info,
  reload,
}: StepProps & { info: Info; reload: () => Promise<void> }) {
  // The stick the installer runs from is hidden; too-small drives go last.
  const disks = info.disks
    .filter((x) => !x.boot)
    .sort((a, b) => Number(a.too_small) - Number(b.too_small));
  const disk = disks.find((x) => x.path === d.disk);
  const [advanced, setAdvanced] = useState(false);
  const [showPass, setShowPass] = useState(false);
  const listRef = useRef<HTMLDivElement>(null);

  // Arrow keys move between the usable disks.
  const usable = disks.filter((x) => !x.too_small);
  const onKey = (e: React.KeyboardEvent) => {
    if (
      e.key !== "ArrowDown" &&
      e.key !== "ArrowUp" &&
      e.key !== "ArrowRight" &&
      e.key !== "ArrowLeft"
    )
      return;
    e.preventDefault();
    const i = usable.findIndex((x) => x.path === d.disk);
    const n =
      e.key === "ArrowDown" || e.key === "ArrowRight"
        ? Math.min(usable.length - 1, i + 1)
        : Math.max(0, i - 1);
    if (usable[n]) {
      set({ disk: usable[n].path });
      (
        listRef.current?.querySelector(
          `[data-path="${usable[n].path}"]`,
        ) as HTMLElement | null
      )?.focus();
    }
  };

  const before =
    disk?.segments.map((s) => ({
      kind: s.kind,
      bytes: s.bytes,
      text: segLabel(t, s),
    })) ?? [];
  const after = disk
    ? [
        {
          kind: "startup",
          bytes: info.layout.startup,
          text: t("in.newStartup", { size: size(info.layout.startup) }),
        },
        {
          kind: "system",
          bytes: info.layout.system,
          text: t("in.newSystem", { size: size(info.layout.system) }),
        },
        {
          kind: "data",
          bytes: disk.bytes - info.layout.startup - info.layout.system,
          text: t("in.newData", {
            size: size(disk.bytes - info.layout.startup - info.layout.system),
          }),
        },
      ]
    : [];
  const s = strength(d.pass);

  return (
    <Card icon="drive" title={t("in.diskTitle")} text={t("in.diskText")} wide>
      <div className={`nci-disk-layout ${disk ? "picked" : ""}`}>
        <div className="nci-disk-col">
          {disks.length === 0 ? (
            <div className="nci-glass nci-empty">
              <p>{t("in.noDisks")}</p>
              <button
                type="button"
                className="nci-btn"
                onClick={() => void reload()}
              >
                {t("in.lookAgain")}
              </button>
            </div>
          ) : (
            <div
              className="nci-disks"
              role="radiogroup"
              aria-label={t("in.diskTitle")}
              ref={listRef}
              onKeyDown={onKey}
            >
              {disks.map((x) => {
                const on = x.path === d.disk;
                return (
                  <div
                    key={x.path}
                    data-path={x.path}
                    role="radio"
                    aria-checked={on}
                    aria-disabled={x.too_small}
                    tabIndex={
                      x.too_small
                        ? -1
                        : on || (!d.disk && x === usable[0])
                          ? 0
                          : -1
                    }
                    className={`nci-disk nci-glass ${on ? "on" : ""} ${x.too_small ? "small" : ""}`}
                    onClick={() => !x.too_small && set({ disk: x.path })}
                    onKeyDown={(e) => {
                      if (
                        (e.key === " " || e.key === "Enter") &&
                        !x.too_small &&
                        !on
                      ) {
                        e.preventDefault();
                        e.stopPropagation();
                        set({ disk: x.path });
                      }
                    }}
                  >
                    <span className={`nci-disk-icon k-${x.kind}`}>
                      <Icon name={KIND_ICON[x.kind]} size={34} />
                    </span>
                    <span className="nci-disk-main">
                      <span className="nci-disk-top">
                        <b>{x.name}</b>
                        <span className="nci-badge">
                          {t(`in.kind.${x.kind}`)}
                        </span>
                        <span className="nci-disk-size">{size(x.bytes)}</span>
                      </span>
                      {x.too_small ? (
                        <span className="nci-disk-why">
                          {t("in.tooSmall", { min: size(info.min_bytes) })}
                        </span>
                      ) : (
                        <Bar
                          t={t}
                          total={x.bytes}
                          labels
                          segments={x.segments.map((sg) => ({
                            kind: sg.kind,
                            bytes: sg.bytes,
                            text: segLabel(t, sg),
                          }))}
                        />
                      )}
                    </span>
                    <span className="nci-radio" aria-hidden>
                      {on && <Icon name="check" size={16} />}
                    </span>
                  </div>
                );
              })}
            </div>
          )}

          <button
            type="button"
            className="nci-link"
            onClick={() => setAdvanced((a) => !a)}
            aria-expanded={advanced}
          >
            {t("in.advanced")}
          </button>
          {advanced && <p className="nci-note">{t("in.advancedNote")}</p>}
        </div>
        {disk && (
          <div className="nci-disk-side">
            <div className="nci-after nci-glass" key={disk.path}>
              <div className="nci-after-row">
                <span className="nci-after-tag">{t("in.now")}</span>
                <Bar t={t} total={disk.bytes} segments={before} />
              </div>
              <Icon name="arrowDown" size={18} className="nci-after-arrow" />
              <div className="nci-after-row">
                <span className="nci-after-tag on">{t("in.after")}</span>
                <Bar t={t} total={disk.bytes} labels segments={after} />
              </div>
              <div className="nci-warning" role="alert">
                <Icon name="alert" size={18} />
                {t("in.eraseWarning", { name: disk.name })}
              </div>
            </div>
            <div className="nci-glass nci-lock">
              <label className="nci-switch-row">
                <span>
                  <b>
                    <Icon name="lock" size={16} /> {t("in.lock")}
                  </b>
                  <small>{t("in.lockHint")}</small>
                </span>
                <input
                  type="checkbox"
                  role="switch"
                  checked={d.encrypt}
                  onChange={(e) => set({ encrypt: e.target.checked })}
                />
              </label>
              <div className={`nci-reveal ${d.encrypt ? "open" : ""}`}>
                {d.encrypt && (
                  <>
                    {info.tpm && (
                      <label className="nci-switch-row sub">
                        <span>
                          <b>{t("in.tpm")}</b>
                          <small>{t("in.tpmHint")}</small>
                        </span>
                        <input
                          type="checkbox"
                          role="switch"
                          checked={d.useTPM}
                          onChange={(e) => set({ useTPM: e.target.checked })}
                        />
                      </label>
                    )}
                    {!(d.useTPM && info.tpm) && (
                      <div className="nci-pass">
                        <label className="nci-field">
                          <span>{t("in.passphrase")}</span>
                          <span className="nci-input-eye">
                            <input
                              type={showPass ? "text" : "password"}
                              value={d.pass}
                              autoComplete="new-password"
                              onChange={(e) => set({ pass: e.target.value })}
                            />
                            <button
                              type="button"
                              className="nci-eye"
                              aria-label={
                                showPass ? t("in.hide") : t("in.show")
                              }
                              onClick={() => setShowPass((v) => !v)}
                            >
                              <Icon name="eye" size={16} />
                            </button>
                          </span>
                        </label>
                        <Meter s={s} t={t} />
                        <label className="nci-field">
                          <span>{t("in.passphraseAgain")}</span>
                          <input
                            type={showPass ? "text" : "password"}
                            value={d.pass2}
                            autoComplete="new-password"
                            onChange={(e) => set({ pass2: e.target.value })}
                          />
                        </label>
                        {d.pass2 && d.pass !== d.pass2 && (
                          <small className="nci-bad">{t("in.noMatch")}</small>
                        )}
                        <small className="nci-hint">
                          {t("in.passphraseHint")}
                        </small>
                      </div>
                    )}
                  </>
                )}
              </div>
            </div>
          </div>
        )}
      </div>
    </Card>
  );
}

function Meter({ s, t }: { s: number; t: T }) {
  const words = [
    t("in.str0"),
    t("in.str1"),
    t("in.str2"),
    t("in.str3"),
    t("in.str4"),
  ];
  return (
    <div className={`nci-meter s${s}`} aria-label={words[s]}>
      <span>
        {[1, 2, 3, 4].map((i) => (
          <i key={i} className={i <= s ? "on" : ""} />
        ))}
      </span>
      <small>{words[s]}</small>
    </div>
  );
}

// ---- 3. Make your account -----------------------------------------------------------

function AccountStep({ d, set, t }: StepProps) {
  const [show, setShow] = useState(false);
  const userErr =
    d.username &&
    (!USER_RE.test(d.username)
      ? t("in.userBad")
      : RESERVED.has(d.username)
        ? t("in.userTaken")
        : "");
  return (
    <Card icon="user" title={t("in.accountTitle")} text={t("in.accountText")}>
      <div className="nci-glass nci-form">
        <label className="nci-field">
          <span>{t("in.fullName")}</span>
          <input
            autoFocus
            value={d.fullName}
            autoComplete="name"
            onChange={(e) => {
              const fullName = e.target.value;
              const username = d.usernameTouched
                ? d.username
                : toUsername(fullName);
              set({
                fullName,
                username,
                hostname: d.hostnameTouched ? d.hostname : toHostname(username),
              });
            }}
          />
        </label>
        <label className="nci-field">
          <span>{t("in.username")}</span>
          <input
            value={d.username}
            autoComplete="username"
            spellCheck={false}
            onChange={(e) => {
              const username = e.target.value.toLowerCase();
              set({
                username,
                usernameTouched: true,
                hostname: d.hostnameTouched ? d.hostname : toHostname(username),
              });
            }}
          />
          {userErr ? (
            <small className="nci-bad">{userErr}</small>
          ) : (
            <small className="nci-hint">{t("in.usernameHint")}</small>
          )}
        </label>
        <label className="nci-field">
          <span>{t("in.password")}</span>
          <span className="nci-input-eye">
            <input
              type={show ? "text" : "password"}
              value={d.password}
              autoComplete="new-password"
              onChange={(e) => set({ password: e.target.value })}
            />
            <button
              type="button"
              className="nci-eye"
              aria-label={show ? t("in.hide") : t("in.show")}
              onClick={() => setShow((v) => !v)}
            >
              <Icon name="eye" size={16} />
            </button>
          </span>
        </label>
        <Meter s={strength(d.password)} t={t} />
        <label className="nci-field">
          <span>{t("in.passwordAgain")}</span>
          <input
            type={show ? "text" : "password"}
            value={d.password2}
            autoComplete="new-password"
            onChange={(e) => set({ password2: e.target.value })}
          />
          {d.password2 && d.password !== d.password2 && (
            <small className="nci-bad">{t("in.noMatch")}</small>
          )}
        </label>
        <label className="nci-field">
          <span>{t("in.computerName")}</span>
          <input
            value={d.hostname}
            spellCheck={false}
            onChange={(e) =>
              set({
                hostname: e.target.value
                  .toLowerCase()
                  .replace(/[^a-z0-9-]/g, ""),
                hostnameTouched: true,
              })
            }
          />
          <small className="nci-hint">
            {t("in.computerNameHint", { name: d.hostname || "nocap" })}
          </small>
        </label>
      </div>
    </Card>
  );
}

// ---- 4. How will you use NoCapOS? -----------------------------------------------------

function UsageStep({ d, set, t }: StepProps) {
  const opts: { id: Usage; icon: IconName; title: string; text: string }[] = [
    {
      id: "desktop-web",
      icon: "devices",
      title: t("in.useBoth"),
      text: t("in.useBothText"),
    },
    {
      id: "desktop",
      icon: "desktop",
      title: t("in.useDesktop"),
      text: t("in.useDesktopText"),
    },
    {
      id: "web",
      icon: "globe",
      title: t("in.useWeb"),
      text: t("in.useWebText"),
    },
  ];
  return (
    <Card icon="sliders" title={t("in.usageTitle")} text={t("in.usageText")}>
      <div
        className="nci-usages"
        role="radiogroup"
        aria-label={t("in.usageTitle")}
      >
        {opts.map((o, i) => (
          <button
            type="button"
            key={o.id}
            role="radio"
            aria-checked={d.usage === o.id}
            autoFocus={d.usage === o.id}
            className={`nci-usage-card nci-glass ${d.usage === o.id ? "on" : ""}`}
            style={{ "--i": i } as CSSProperties}
            onClick={() => set({ usage: o.id })}
          >
            <Icon name={o.icon} size={34} />
            <b>{o.title}</b>
            <span>{o.text}</span>
            {i === 0 && <em className="nci-badge">{t("in.recommended")}</em>}
          </button>
        ))}
      </div>
      <p className="nci-note">{t("in.usageLater")}</p>
    </Card>
  );
}

// ---- 5. Review ----------------------------------------------------------------------

function ReviewStep({
  d,
  t,
  disk,
  tpm,
  understood,
  setUnderstood,
  err,
  onEdit,
}: {
  d: Draft;
  t: T;
  disk: Disk;
  tpm: boolean;
  understood: boolean;
  setUnderstood: (v: boolean) => void;
  err: string;
  onEdit: (s: StepId) => void;
}) {
  const kb = KEYBOARDS.find((k) => k.id === d.keyboard)?.name ?? d.keyboard;
  const usage = {
    "desktop-web": t("in.useBoth"),
    desktop: t("in.useDesktop"),
    web: t("in.useWeb"),
  }[d.usage];
  const rows: { step: StepId; label: string; value: string }[] = [
    {
      step: "language",
      label: t("in.rvRegion"),
      value: `${d.zone.replace(/_/g, " ")} · ${kb}`,
    },
    {
      step: "disk",
      label: t("in.rvDisk"),
      value: `${disk.name} · ${size(disk.bytes)}`,
    },
    {
      step: "disk",
      label: t("in.rvLock"),
      value: d.encrypt
        ? tpm && d.useTPM
          ? t("in.rvLockTPM")
          : t("in.rvLockPass")
        : t("in.rvLockOff"),
    },
    {
      step: "account",
      label: t("in.rvAccount"),
      value: `${d.fullName.trim()} (${d.username})`,
    },
    { step: "account", label: t("in.computerName"), value: d.hostname },
    { step: "usage", label: t("in.rvUsage"), value: usage },
  ];
  return (
    <Card icon="list" title={t("in.reviewTitle")} text={t("in.reviewText")}>
      <div className="nci-glass nci-review">
        {rows.map((r, i) => (
          <div
            className="nci-review-row"
            key={i}
            style={{ "--i": i } as CSSProperties}
          >
            <span>{r.label}</span>
            <b>{r.value}</b>
            <button
              type="button"
              className="nci-link"
              onClick={() => onEdit(r.step)}
            >
              {t("in.change")}
            </button>
          </div>
        ))}
      </div>
      <div className="nci-warning big" role="alert">
        <Icon name="alert" size={20} />
        {t("in.eraseWarning", { name: disk.name })}
      </div>
      <label className="nci-understand">
        <input
          type="checkbox"
          checked={understood}
          onChange={(e) => setUnderstood(e.target.checked)}
        />
        <span>{t("in.understand", { name: disk.name })}</span>
      </label>
      {err && <p className="nci-bad">{err}</p>}
    </Card>
  );
}

// ---- 6. Installing / Restart now -------------------------------------------------------

const SLIDES: { icon: IconName; key: string }[] = [
  { icon: "store", key: "in.slideApps" },
  { icon: "files", key: "in.slideFiles" },
  { icon: "shield", key: "in.slideShield" },
  { icon: "phone", key: "in.slideLink" },
  { icon: "restart", key: "in.slideUpdates" },
];

function InstallStep({ prog, t }: { prog: Progress | null; t: T }) {
  const [slide, setSlide] = useState(0);
  const [restarting, setRestarting] = useState(false);
  useEffect(() => {
    const iv = window.setInterval(
      () => setSlide((s) => (s + 1) % SLIDES.length),
      5000,
    );
    return () => window.clearInterval(iv);
  }, []);
  if (prog?.error) {
    return (
      <Card icon="alert" title={t("in.failedTitle")} text={t("in.failedText")}>
        <pre className="nci-glass nci-error">{prog.error}</pre>
        <button
          type="button"
          className="nci-btn"
          onClick={() => void call("/api/installer/poweroff", {})}
        >
          {t("in.powerOff")}
        </button>
      </Card>
    );
  }
  if (prog?.done) {
    return (
      <Card icon="check" title={t("in.doneTitle")} text={t("in.doneText")}>
        {prog.recovery_key && (
          <div className="nci-glass nci-recovery">
            <b>{t("in.recoveryTitle")}</b>
            <code>{prog.recovery_key}</code>
            <small>{t("in.recoveryText")}</small>
          </div>
        )}
        <button
          type="button"
          className="nci-btn primary big"
          autoFocus
          disabled={restarting}
          onClick={() => {
            setRestarting(true);
            void call("/api/installer/restart", {});
          }}
        >
          {restarting ? (
            <span className="spinner sm" />
          ) : (
            <Icon name="restart" size={20} />
          )}{" "}
          {t("in.restartNow")}
        </button>
      </Card>
    );
  }
  const pct = prog?.percent ?? 0;
  const s = SLIDES[slide];
  return (
    <section className="nci-step nci-installing">
      <div className="nci-ring" style={{ "--p": pct / 100 } as CSSProperties}>
        <span>{pct}%</span>
      </div>
      <h1>{t("in.installing")}</h1>
      <p className="nci-lead">{t(`in.phase.${prog?.step ?? "prepare"}`)}</p>
      <div className="nci-slide nci-glass" key={slide}>
        <Icon name={s.icon} size={30} />
        <span>{t(s.key)}</span>
      </div>
    </section>
  );
}
