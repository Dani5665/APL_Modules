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
      this.haynesPro = d.haynesPro === 'true';
      this.tier = d.tier || 'BUSINESS';
      this.months = d.months || '1';
      if (this.testPeriod) {
        this.applyTestPeriod();
      }
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
    },

    applyTestPeriod() {
      if (this.previous === null) {
        this.previous = {
          fastCalculator: this.fastCalculator,
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
