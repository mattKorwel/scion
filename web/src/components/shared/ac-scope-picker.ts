/**
 * Copyright 2026 Google LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

/**
 * scion-ac-scope-picker — combobox for AC (alteredCarbon) scope selection.
 *
 * Why this exists: scope is a path like "amplify/managed-claw/hosted-
 * service". Operators have 30+ scopes (and growing); typing the full
 * path from memory every time is painful. A combobox that loads from
 * /api/v1/ac/scopes, type-ahead filters, shows recents at the top,
 * and accepts free-text-as-new is the right ergonomic.
 *
 * Behavior:
 *   - On focus / first interaction, fetches /api/v1/ac/scopes (cached
 *     30s on the hub). Shows results in a dropdown menu below the input.
 *   - Recents (per-browser localStorage, last 8 unique scopes) appear
 *     at the top of the dropdown under a "Recents" header.
 *   - Type-ahead does case-insensitive substring filtering.
 *   - When the typed value doesn't match any existing scope, a "+
 *     Create amplify/<typed>" menu item appears at the bottom for
 *     one-click creation. Submit on this option emits an event with
 *     a `createIfMissing: true` flag so the parent form can route to
 *     /api/v1/ac/scopes (POST) before agent create.
 *   - Free-text (whatever the input contains) is always the "value"
 *     of the picker; the dropdown is purely a navigator.
 *   - Empty value is valid and means "no AC scope binding" (matches
 *     prior input behavior).
 *
 * Dispatches:
 *   - sl-input: native event from the inner <sl-input>; consumers can
 *     listen for live typing.
 *   - ac-scope-change: { detail: { value: string, isNew: boolean } }
 *     fired when the user picks from the dropdown or types and blurs.
 *     isNew=true when the value didn't match any known scope at the
 *     time of selection.
 *
 * Properties:
 *   - value: current scope string (two-way binding via .value=${} +
 *     @ac-scope-change handler)
 *   - placeholder: input placeholder
 *   - disabled: forwards to inner input
 *
 * Recent-scopes ledger lives in localStorage key `scion.ac_scope_recents`.
 * Cap of 8 keeps the menu uncluttered without losing the working set
 * for typical multi-project operators.
 */

import { LitElement, html, css, nothing } from 'lit';
import { customElement, property, state } from 'lit/decorators.js';
import { apiFetch } from '../../client/api.js';

const RECENTS_KEY = 'scion.ac_scope_recents';
const RECENTS_CAP = 8;

/**
 * Read recents from localStorage. Returns [] on parse error or
 * unavailable storage (private mode).
 */
function loadRecents(): string[] {
  try {
    const raw = localStorage.getItem(RECENTS_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter((x): x is string => typeof x === 'string').slice(0, RECENTS_CAP);
  } catch {
    return [];
  }
}

/**
 * Push a scope to the recents list (move-to-front, dedupe, cap).
 * Safe to call with empty string — no-op. Storage failures are
 * silent; recents are best-effort.
 */
export function pushRecentScope(scope: string): void {
  const s = scope.trim();
  if (!s) return;
  try {
    const cur = loadRecents();
    const filtered = cur.filter((x) => x !== s);
    const next = [s, ...filtered].slice(0, RECENTS_CAP);
    localStorage.setItem(RECENTS_KEY, JSON.stringify(next));
  } catch {
    // ignore
  }
}

interface ScopeListResponse {
  scopes: string[];
  cached: boolean;
}

@customElement('scion-ac-scope-picker')
export class ScionACScopePicker extends LitElement {
  /** Current scope value (free-text, may be empty). */
  @property({ type: String })
  value = '';

  @property({ type: String })
  placeholder = 'amplify/my-project';

  @property({ type: Boolean })
  disabled = false;

  @state()
  private scopes: string[] = [];

  @state()
  private recents: string[] = [];

  @state()
  private loading = false;

  @state()
  private loadError = '';

  @state()
  private open = false;

  /**
   * Filter text. Initialized from value on mount; updated as user types.
   * Kept separate from value so we can show what's IN the box vs. what's
   * BEEN PICKED. (Today they're the same since we don't have a "selected"
   * state distinct from "typed", but this leaves room for future
   * "show selected chip + edit" UX.)
   */
  @state()
  private query = '';

  static override styles = css`
    :host {
      display: block;
      position: relative;
    }

    .menu {
      position: absolute;
      top: 100%;
      left: 0;
      right: 0;
      margin-top: 0.25rem;
      background: var(--sl-color-neutral-0, white);
      border: 1px solid var(--sl-color-neutral-200, #e2e8f0);
      border-radius: var(--sl-border-radius-medium, 0.375rem);
      box-shadow: 0 4px 12px rgba(0, 0, 0, 0.08);
      max-height: 18rem;
      overflow-y: auto;
      z-index: 50;
      font-size: 0.875rem;
    }

    .menu-section-header {
      padding: 0.375rem 0.625rem;
      font-size: 0.6875rem;
      text-transform: uppercase;
      letter-spacing: 0.05em;
      color: var(--scion-text-muted, #64748b);
      background: var(--sl-color-neutral-50, #f8fafc);
      border-bottom: 1px solid var(--sl-color-neutral-100, #f1f5f9);
    }

    .menu-item {
      padding: 0.375rem 0.625rem;
      cursor: pointer;
      display: flex;
      align-items: center;
      gap: 0.5rem;
      font-family: var(--sl-font-mono, monospace);
      font-size: 0.8125rem;
      color: var(--scion-text, #1e293b);
    }

    .menu-item:hover,
    .menu-item.highlight {
      background: var(--sl-color-primary-50, #eff6ff);
      color: var(--sl-color-primary-700, #1d4ed8);
    }

    .menu-item.create {
      color: var(--sl-color-success-700, #047857);
      font-family: var(--sl-font-sans, system-ui);
      font-weight: 500;
      border-top: 1px solid var(--sl-color-neutral-100, #f1f5f9);
    }

    .menu-empty,
    .menu-error,
    .menu-loading {
      padding: 0.5rem 0.625rem;
      color: var(--scion-text-muted, #64748b);
      font-size: 0.8125rem;
      font-style: italic;
    }

    .menu-error {
      color: var(--sl-color-danger-700, #b91c1c);
    }
  `;

  override connectedCallback(): void {
    super.connectedCallback();
    this.recents = loadRecents();
    this.query = this.value;
    // Listen for outside clicks so the menu closes when the operator
    // clicks somewhere other than us. Bound here so the same fn
    // reference can be removed on disconnect.
    this._onDocClick = this._onDocClick.bind(this);
    document.addEventListener('mousedown', this._onDocClick);
  }

  override disconnectedCallback(): void {
    super.disconnectedCallback();
    document.removeEventListener('mousedown', this._onDocClick);
  }

  private _onDocClick(e: MouseEvent): void {
    // If the click was inside this element, leave the menu open.
    // Otherwise close. composedPath() crosses shadow DOM boundaries.
    const path = e.composedPath();
    if (!path.includes(this)) {
      this.open = false;
    }
  }

  /**
   * Lazy-load the scope list the first time the menu opens. Subsequent
   * opens reuse the cached list; the operator can click into the box
   * many times in a session without refetching. Use refresh() to force.
   */
  private async ensureScopesLoaded(): Promise<void> {
    if (this.scopes.length > 0 || this.loading) return;
    this.loading = true;
    this.loadError = '';
    try {
      const res = await apiFetch('/api/v1/ac/scopes');
      if (!res.ok) {
        const body = await res.text().catch(() => '');
        this.loadError = `Failed to load AC scopes (HTTP ${res.status}). ${body.slice(0, 120)}`;
        return;
      }
      const data = (await res.json()) as ScopeListResponse;
      this.scopes = Array.isArray(data.scopes) ? data.scopes : [];
    } catch (err) {
      this.loadError = `Failed to reach AC scope proxy: ${(err as Error).message || err}`;
    } finally {
      this.loading = false;
    }
  }

  /** Force a fresh fetch (bypasses hub-side cache). */
  async refresh(): Promise<void> {
    this.scopes = [];
    this.loading = true;
    this.loadError = '';
    try {
      const res = await apiFetch('/api/v1/ac/scopes?no_cache=1');
      if (!res.ok) {
        this.loadError = `HTTP ${res.status}`;
        return;
      }
      const data = (await res.json()) as ScopeListResponse;
      this.scopes = Array.isArray(data.scopes) ? data.scopes : [];
    } finally {
      this.loading = false;
    }
  }

  private onFocus = (): void => {
    this.open = true;
    void this.ensureScopesLoaded();
  };

  private onInput = (e: Event): void => {
    const v = (e.target as HTMLElement & { value: string }).value;
    this.query = v;
    this.value = v;
    this.open = true;
    void this.ensureScopesLoaded();
    this.dispatchEvent(
      new CustomEvent('ac-scope-change', {
        detail: { value: v, isNew: this.isNew(v) },
        bubbles: true,
        composed: true,
      })
    );
  };

  /** True when `s` doesn't match any known scope (case-sensitive). */
  private isNew(s: string): boolean {
    const t = s.trim();
    if (!t) return false; // empty is "no binding", not "new"
    return !this.scopes.includes(t);
  }

  private selectScope(scope: string, isNew: boolean): void {
    this.value = scope;
    this.query = scope;
    this.open = false;
    this.dispatchEvent(
      new CustomEvent('ac-scope-change', {
        detail: { value: scope, isNew },
        bubbles: true,
        composed: true,
      })
    );
  }

  /** Filter the canonical scope list against the current query. */
  private get filteredScopes(): string[] {
    const q = this.query.trim().toLowerCase();
    if (!q) return this.scopes;
    return this.scopes.filter((s) => s.toLowerCase().includes(q));
  }

  /** Recents intersected with the canonical list (drop stale entries). */
  private get visibleRecents(): string[] {
    const q = this.query.trim().toLowerCase();
    return this.recents.filter((r) => {
      if (q && !r.toLowerCase().includes(q)) return false;
      return this.scopes.includes(r);
    });
  }

  private renderMenu() {
    if (!this.open) return nothing;
    if (this.loading && this.scopes.length === 0) {
      return html`<div class="menu"><div class="menu-loading">Loading scopes…</div></div>`;
    }
    if (this.loadError) {
      return html`<div class="menu"><div class="menu-error">${this.loadError}</div></div>`;
    }

    const recents = this.visibleRecents;
    const filtered = this.filteredScopes;
    // Don't duplicate recents in the all-scopes section.
    const filteredMinusRecents = filtered.filter((s) => !recents.includes(s));
    const typed = this.query.trim();
    const showCreate = typed.length > 0 && this.isNew(typed);

    const empty =
      recents.length === 0 &&
      filteredMinusRecents.length === 0 &&
      !showCreate;

    return html`
      <div class="menu" role="listbox">
        ${recents.length > 0
          ? html`
              <div class="menu-section-header">Recents</div>
              ${recents.map(
                (s) => html`
                  <div
                    class="menu-item"
                    role="option"
                    @mousedown=${(e: MouseEvent) => {
                      // Use mousedown so we beat the document mousedown
                      // close-handler that fires before click.
                      e.preventDefault();
                      this.selectScope(s, false);
                    }}
                  >
                    <sl-icon name="clock-history"></sl-icon>
                    ${s}
                  </div>
                `
              )}
            `
          : nothing}
        ${filteredMinusRecents.length > 0
          ? html`
              ${recents.length > 0
                ? html`<div class="menu-section-header">All scopes</div>`
                : nothing}
              ${filteredMinusRecents.map(
                (s) => html`
                  <div
                    class="menu-item"
                    role="option"
                    @mousedown=${(e: MouseEvent) => {
                      e.preventDefault();
                      this.selectScope(s, false);
                    }}
                  >
                    ${s}
                  </div>
                `
              )}
            `
          : nothing}
        ${showCreate
          ? html`
              <div
                class="menu-item create"
                role="option"
                @mousedown=${(e: MouseEvent) => {
                  e.preventDefault();
                  this.selectScope(typed, true);
                }}
              >
                <sl-icon name="plus-circle"></sl-icon>
                Create new scope <code>${typed}</code>
              </div>
            `
          : nothing}
        ${empty
          ? html`<div class="menu-empty">
              ${typed
                ? html`No scopes match <code>${typed}</code>.`
                : html`(no scopes; create one above)`}
            </div>`
          : nothing}
      </div>
    `;
  }

  override render() {
    return html`
      <sl-input
        placeholder=${this.placeholder}
        ?disabled=${this.disabled}
        .value=${this.value}
        @sl-focus=${this.onFocus}
        @sl-input=${this.onInput}
        autocomplete="off"
        spellcheck="false"
      >
        <sl-icon slot="prefix" name="bullseye"></sl-icon>
      </sl-input>
      ${this.renderMenu()}
    `;
  }
}

declare global {
  interface HTMLElementTagNameMap {
    'scion-ac-scope-picker': ScionACScopePicker;
  }
}
