/* Alpine components and small helpers.
 *
 * Alpine ships in its CSP build, which evaluates no expressions at runtime.
 * That means every x-* attribute may only name a property or a method of the
 * component below - no inline JavaScript anywhere - which is what lets the
 * Content-Security-Policy stay at script-src 'self'.
 */

document.addEventListener('alpine:init', () => {
  /* The salesperson's request form, and the admin's edit of the same data. */
  Alpine.data('requestForm', () => ({
    testPeriod: false,
    fastCalculator: false,
    serviceData: false,
    haynesPro: false,
    tier: 'BUSINESS',
    months: '1',
    testPeriodUsed: false,
    search: '',

    /* Remembers the editable state while a test period overrides it, so
     * unchecking the box restores what the user had chosen. */
    previous: null,

    init() {
      const d = this.$el.dataset;
      this.testPeriodUsed = d.testPeriodUsed === 'true';
      this.testPeriod = d.testPeriod === 'true';
      this.fastCalculator = d.fastCalculator === 'true';
      this.serviceData = d.serviceData === 'true';
      this.haynesPro = d.haynesPro === 'true';
      this.tier = d.tier || 'BUSINESS';
      this.months = d.months || '1';
      if (this.testPeriod) {
        this.applyTestPeriod();
      }
      this.syncServiceData();
    },

    /* Business and Ultimate already include Servicedata, so its box is
     * unchecked and locked while one of them is selected. */
    get serviceDataIncluded() {
      const tier = this.testPeriod ? 'BUSINESS' : this.tier;
      return (this.testPeriod || this.haynesPro) && (tier === 'BUSINESS' || tier === 'ULTRA');
    },
    get serviceDataLocked() {
      return this.locked || this.serviceDataIncluded;
    },
    syncServiceData() {
      if (this.serviceDataIncluded) this.serviceData = false;
    },

    /* locked is true while a test period fixes the module selection. */
    get locked() {
      return this.testPeriod;
    },

    /* The hidden mirrors below carry the effective values, because a
     * disabled input is not submitted by the browser. The server re-applies
     * these rules regardless of what arrives. */
    get fastCalculatorValue() {
      return (this.testPeriod || this.fastCalculator) ? '1' : '';
    },
    get serviceDataValue() {
      return (!this.serviceDataIncluded && this.serviceData) ? '1' : '';
    },
    get haynesProValue() {
      return (this.testPeriod || this.haynesPro) ? '1' : '';
    },
    get tierValue() {
      if (this.testPeriod) return 'BUSINESS';
      return this.haynesPro ? this.tier : '';
    },
    get monthsValue() {
      return this.testPeriod ? '1' : this.months;
    },
    get testPeriodValue() {
      return this.testPeriod ? '1' : '';
    },

    /* The tier dropdown appears only with HaynesPro selected. */
    get showTier() {
      return this.testPeriod || this.haynesPro;
    },

    onTestPeriodChange() {
      if (this.testPeriod) {
        this.applyTestPeriod();
      } else {
        this.restorePrevious();
      }
      this.syncServiceData();
    },

    applyTestPeriod() {
      if (this.previous === null) {
        this.previous = {
          fastCalculator: this.fastCalculator,
          serviceData: this.serviceData,
          haynesPro: this.haynesPro,
          tier: this.tier,
          months: this.months,
        };
      }
      this.fastCalculator = true;
      this.haynesPro = true;
      this.tier = 'BUSINESS';
      this.months = '1';
    },

    restorePrevious() {
      if (this.previous === null) return;
      this.fastCalculator = this.previous.fastCalculator;
      this.serviceData = this.previous.serviceData;
      this.haynesPro = this.previous.haynesPro;
      this.tier = this.previous.tier;
      this.months = this.previous.months;
      this.previous = null;
    },

    /* Username picker. Filtering touches the DOM directly because the CSP
     * build cannot evaluate a filter expression in a template. */
    filterUsers() {
      const needle = this.search.trim().toLowerCase();
      const list = this.$refs.userList;
      if (!list) return;
      list.querySelectorAll('[data-username]').forEach((row) => {
        const name = row.dataset.username.toLowerCase();
        row.hidden = needle !== '' && !name.includes(needle);
      });
    },

    selectAllUsers() {
      this.setAllUsers(true);
    },

    clearAllUsers() {
      this.setAllUsers(false);
    },

    setAllUsers(checked) {
      const list = this.$refs.userList;
      if (!list) return;
      list.querySelectorAll('input[type="checkbox"]').forEach((box) => {
        // Only rows the search currently shows are affected.
        const row = box.closest('[data-username]');
        if (row && row.hidden) return;
        box.checked = checked;
      });
    },
  }));

  /* A disclosure panel, used by the placeholder info button. */
  Alpine.data('disclosure', () => ({
    open: false,
    toggle() {
      this.open = !this.open;
    },
    get expanded() {
      return this.open ? 'true' : 'false';
    },
  }));

  /* A confirmation prompt on a destructive form. */
  Alpine.data('confirmForm', () => ({
    confirm(event) {
      const message = this.$el.dataset.confirm || 'Сигурни ли сте?';
      if (!window.confirm(message)) {
        event.preventDefault();
      }
    },
  }));

  /* The manual activation form: the tier dropdown follows the module. */
  Alpine.data('activationForm', () => ({
    module: 'FAST_CALCULATOR',
    init() {
      this.module = this.$el.dataset.module || 'FAST_CALCULATOR';
    },
    get showTier() {
      return this.module === 'HAYNESPRO';
    },
  }));
});

/* htmx's default config does not swap a 4xx/5xx response into the page at
 * all (see responseHandling in htmx.min.js) - it just fires htmx:responseError
 * and drops the body. Every error response this application sends to an
 * htmx request is a deliberately rendered fragment meant to be shown (a form
 * re-rendered with validation messages, a "session expired" notice, a
 * generic error alert) - not raw error text - so those responses must be
 * swapped in like any other. Without this, a rejected submission (wrong
 * module selection, an expired CSRF token, ...) looks like nothing happened
 * at all: the button flashes disabled and the page silently keeps the old,
 * unchanged form with no explanation.
 */
document.body.addEventListener('htmx:beforeSwap', (evt) => {
  if (evt.detail.xhr.status >= 400) {
    evt.detail.shouldSwap = true;
  }
});

/* Copy buttons, popup dialogs and the username status. Delegated from the
 * document, so they keep working on content that htmx swaps in. */

/* Copies text, falling back to a hidden textarea where the async clipboard
 * API is unavailable (plain-HTTP origins other than localhost). */
function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    return navigator.clipboard.writeText(text).then(() => true, () => false);
  }
  return new Promise((resolve) => {
    const ta = document.createElement('textarea');
    ta.value = text;
    ta.setAttribute('readonly', '');
    ta.style.position = 'fixed';
    ta.style.opacity = '0';
    document.body.appendChild(ta);
    ta.select();
    let ok = false;
    try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
    document.body.removeChild(ta);
    resolve(ok);
  });
}

document.addEventListener('click', (e) => {
  const copy = e.target.closest('[data-copy]');
  if (copy) {
    if (copy.disabled) return;
    copyText(copy.dataset.copy).then((ok) => {
      const status = document.getElementById('copy-status');
      if (status) status.textContent = ok ? 'Копирано' : 'Копирането не беше възможно';
      copy.classList.toggle('copied', ok);
      setTimeout(() => {
        copy.classList.remove('copied');
        if (status) status.textContent = '';
      }, 1500);
    });
    return;
  }

  const open = e.target.closest('[data-dialog-open]');
  if (open) {
    const d = document.getElementById(open.dataset.dialogOpen);
    if (d && typeof d.showModal === 'function') d.showModal();
    return;
  }

  const close = e.target.closest('[data-dialog-close]');
  if (close) {
    const d = close.closest('dialog');
    if (d) d.close();
  }
});

/* A dialog that closes after a successful submission reloads the page, so the
 * next time it opens it shows a fresh, empty form. 'close' does not bubble,
 * hence the capture phase. */
document.addEventListener('close', (e) => {
  if (e.target.tagName === 'DIALOG' && e.target.querySelector('[data-done]')) {
    window.location.reload();
  }
}, true);

/* Typing in a field clears the status shown beside it (data-clears names the
 * status element), so a stale check result never lingers. */
document.addEventListener('input', (e) => {
  const id = e.target.dataset && e.target.dataset.clears;
  if (!id) return;
  const el = document.getElementById(id);
  if (el) el.textContent = '';
});
