(function () {
  const doc = document;
  const DEFAULT_THEME = 'occult-umbral';
  const DEFAULT_ACCENT = '#e6c27a';
  const MAX_CUSTOM_CSS_BYTES = 64 * 1024;
  const accentNames = [
    ['rosewater', 'Rosewater'], ['flamingo', 'Flamingo'], ['pink', 'Pink'], ['mauve', 'Mauve'],
    ['red', 'Red'], ['maroon', 'Maroon'], ['peach', 'Peach'], ['yellow', 'Yellow'],
    ['green', 'Green'], ['teal', 'Teal'], ['sky', 'Sky'], ['sapphire', 'Sapphire'],
    ['blue', 'Blue'], ['lavender', 'Lavender']
  ];
  const themes = {
    system: theme('System', false, true, '#f38ba8', '#eba0ac', {
      rosewater:'#f5e0dc', flamingo:'#f2cdcd', pink:'#f5c2e7', mauve:'#cba6f7', red:'#f38ba8', maroon:'#eba0ac', peach:'#fab387', yellow:'#f9e2af', green:'#a6e3a1', teal:'#94e2d5', sky:'#89dceb', sapphire:'#74c7ec', blue:'#89b4fa', lavender:'#b4befe',
      text:'#cdd6f4', subtext1:'#bac2de', subtext0:'#a6adc8', overlay2:'#9399b2', overlay1:'#7f849c', overlay0:'#6c7086', surface2:'#585b70', surface1:'#45475a', surface0:'#313244', base:'#1e1e2e', mantle:'#181825', crust:'#11111b'
    }),
    'catppuccin-mocha': theme('Catppuccin Mocha', true, true, '#f38ba8', '#eba0ac', {
      rosewater:'#f5e0dc', flamingo:'#f2cdcd', pink:'#f5c2e7', mauve:'#cba6f7', red:'#f38ba8', maroon:'#eba0ac', peach:'#fab387', yellow:'#f9e2af', green:'#a6e3a1', teal:'#94e2d5', sky:'#89dceb', sapphire:'#74c7ec', blue:'#89b4fa', lavender:'#b4befe',
      text:'#cdd6f4', subtext1:'#bac2de', subtext0:'#a6adc8', overlay2:'#9399b2', overlay1:'#7f849c', overlay0:'#6c7086', surface2:'#585b70', surface1:'#45475a', surface0:'#313244', base:'#1e1e2e', mantle:'#181825', crust:'#11111b'
    }),
    'catppuccin-macchiato': theme('Catppuccin Macchiato', true, true, '#ed8796', '#ee99a0', {
      rosewater:'#f4dbd6', flamingo:'#f0c6c6', pink:'#f5bde6', mauve:'#c6a0f6', red:'#ed8796', maroon:'#ee99a0', peach:'#f5a97f', yellow:'#eed49f', green:'#a6da95', teal:'#8bd5ca', sky:'#91d7e3', sapphire:'#7dc4e4', blue:'#8aadf4', lavender:'#b7bdf8',
      text:'#cad3f5', subtext1:'#b8c0e0', subtext0:'#a5adcb', overlay2:'#939ab7', overlay1:'#8087a2', overlay0:'#6e738d', surface2:'#5b6078', surface1:'#494d64', surface0:'#363a4f', base:'#24273a', mantle:'#1e2030', crust:'#181926'
    }),
    'catppuccin-frappe': theme('Catppuccin Frappé', true, true, '#e78284', '#ea999c', {
      rosewater:'#f2d5cf', flamingo:'#eebebe', pink:'#f4b8e4', mauve:'#ca9ee6', red:'#e78284', maroon:'#ea999c', peach:'#ef9f76', yellow:'#e5c890', green:'#a6d189', teal:'#81c8be', sky:'#99d1db', sapphire:'#85c1dc', blue:'#8caaee', lavender:'#babbf1',
      text:'#c6d0f5', subtext1:'#b5bfe2', subtext0:'#a5adce', overlay2:'#949cbb', overlay1:'#838ba7', overlay0:'#737994', surface2:'#626880', surface1:'#51576d', surface0:'#414559', base:'#303446', mantle:'#292c3c', crust:'#232634'
    }),
    'catppuccin-latte': theme('Catppuccin Latte', true, false, '#d20f39', '#e64553', {
      rosewater:'#dc8a78', flamingo:'#dd7878', pink:'#ea76cb', mauve:'#8839ef', red:'#d20f39', maroon:'#e64553', peach:'#fe640b', yellow:'#df8e1d', green:'#40a02b', teal:'#179299', sky:'#04a5e5', sapphire:'#209fb5', blue:'#1e66f5', lavender:'#7287fd',
      text:'#4c4f69', subtext1:'#5c5f77', subtext0:'#6c6f85', overlay2:'#7c7f93', overlay1:'#8c8fa1', overlay0:'#9ca0b0', surface2:'#acb0be', surface1:'#bcc0cc', surface0:'#ccd0da', base:'#eff1f5', mantle:'#e6e9ef', crust:'#dce0e8'
    }),
    dracula: theme('Dracula', false, true, '#bd93f9', '#ff79c6', {red:'#ff5555', maroon:'#ff6e6e', peach:'#ffb86c', yellow:'#f1fa8c', green:'#50fa7b', teal:'#8be9fd', sky:'#8be9fd', sapphire:'#8be9fd', blue:'#6272a4', mauve:'#bd93f9', pink:'#ff79c6', lavender:'#bd93f9', rosewater:'#f8f8f2', flamingo:'#ffb3d9', text:'#f8f8f2', subtext1:'#e6e6e6', subtext0:'#cfcfd7', overlay2:'#a5adc6', overlay1:'#858ba3', overlay0:'#6272a4', surface2:'#6272a4', surface1:'#44475a', surface0:'#343746', base:'#282a36', mantle:'#21222c', crust:'#191a21'}),
    'occult-umbral': theme('Occult Umbral', false, true, '#8b2e2e', '#a83a3a', {red:'#c25b5b', maroon:'#a83a3a', peach:'#e6c27a', yellow:'#e6c27a', green:'#8baa82', teal:'#95b3b0', sky:'#95b3b0', sapphire:'#6270a8', blue:'#6270a8', mauve:'#9a7398', pink:'#9a7398', lavender:'#9a7398', rosewater:'#f2eadf', flamingo:'#c25b5b', text:'#e4ded2', subtext1:'#cfc8bb', subtext0:'#aba397', overlay2:'#6f6a7d', overlay1:'#5b5b75', overlay0:'#3a3a4a', surface2:'#2a2a38', surface1:'#1c1c28', surface0:'#14141e', base:'#0a0a12', mantle:'#0f0f18', crust:'#040407'}),
    'ayu-dark': theme('Ayu Dark', false, true, '#ffb454', '#f07178', {red:'#f07178', maroon:'#ff8f40', peach:'#ff8f40', yellow:'#ffee99', green:'#aad94c', teal:'#95e6cb', sky:'#59c2ff', sapphire:'#39bae6', blue:'#59c2ff', mauve:'#d2a6ff', pink:'#ff77aa', lavender:'#b8b4ff', rosewater:'#e6b673', flamingo:'#f29e74', text:'#e6e1cf', subtext1:'#b8cfe6', subtext0:'#a6accd', overlay2:'#7f8a99', overlay1:'#6c7680', overlay0:'#5c6773', surface2:'#3a4350', surface1:'#2d3340', surface0:'#1f2430', base:'#0f1419', mantle:'#0b1015', crust:'#090d12'}),
    'github-dark': theme('GitHub Dark', false, true, '#58a6ff', '#bc8cff', {red:'#f85149', maroon:'#ff7b72', peach:'#d29922', yellow:'#d29922', green:'#3fb950', teal:'#39c5cf', sky:'#79c0ff', sapphire:'#58a6ff', blue:'#58a6ff', mauve:'#bc8cff', pink:'#db61a2', lavender:'#a5d6ff', rosewater:'#ffa198', flamingo:'#ffb3ad', text:'#c9d1d9', subtext1:'#b1bac4', subtext0:'#8b949e', overlay2:'#7d8590', overlay1:'#6e7681', overlay0:'#484f58', surface2:'#30363d', surface1:'#21262d', surface0:'#161b22', base:'#0d1117', mantle:'#010409', crust:'#010409'}),
    'github-light': theme('GitHub Light', false, false, '#0969da', '#8250df', {red:'#cf222e', maroon:'#a40e26', peach:'#bc4c00', yellow:'#9a6700', green:'#1a7f37', teal:'#3192aa', sky:'#0969da', sapphire:'#0969da', blue:'#0969da', mauve:'#8250df', pink:'#bf3989', lavender:'#6639ba', rosewater:'#953800', flamingo:'#cf222e', text:'#24292f', subtext1:'#57606a', subtext0:'#6e7781', overlay2:'#8c959f', overlay1:'#afb8c1', overlay0:'#d0d7de', surface2:'#d8dee4', surface1:'#eaeef2', surface0:'#f6f8fa', base:'#ffffff', mantle:'#f6f8fa', crust:'#f0f3f6'}),
    'green-eyes': theme('Green Eyes', false, true, '#a0d57a', '#a0cfce', {red:'#ffb4ab', maroon:'#ffdad6', peach:'#d9e7ca', yellow:'#bdcbaf', green:'#a0d57a', teal:'#a0cfce', sky:'#bbecea', sapphire:'#a0cfce', blue:'#a0cfce', mauve:'#bdcbaf', pink:'#d9e7ca', lavender:'#bdcbaf', rosewater:'#d9e7ca', flamingo:'#ffdad6', text:'#e3e3dc', subtext1:'#c4c8bb', subtext0:'#a7ab9f', overlay2:'#8e9286', overlay1:'#6a6e63', overlay0:'#44483e', surface2:'#343531', surface1:'#292b26', surface0:'#1e201c', base:'#121410', mantle:'#1a1c18', crust:'#0b0d09'}),
    nord: theme('Nord', false, true, '#88c0d0', '#8fbcbb', {red:'#bf616a', maroon:'#d08770', peach:'#d08770', yellow:'#ebcb8b', green:'#a3be8c', teal:'#8fbcbb', sky:'#88c0d0', sapphire:'#81a1c1', blue:'#5e81ac', mauve:'#b48ead', pink:'#b48ead', lavender:'#b48ead', rosewater:'#d8dee9', flamingo:'#e5e9f0', text:'#eceff4', subtext1:'#e5e9f0', subtext0:'#d8dee9', overlay2:'#a9b5c5', overlay1:'#81a1c1', overlay0:'#6f7d91', surface2:'#4c566a', surface1:'#434c5e', surface0:'#3b4252', base:'#2e3440', mantle:'#242933', crust:'#1f232c'}),
    'gruvbox-dark': theme('Gruvbox Dark', false, true, '#fabd2f', '#fe8019', {red:'#fb4934', maroon:'#cc241d', peach:'#fe8019', yellow:'#fabd2f', green:'#b8bb26', teal:'#8ec07c', sky:'#83a598', sapphire:'#458588', blue:'#83a598', mauve:'#d3869b', pink:'#d3869b', lavender:'#b16286', rosewater:'#ebdbb2', flamingo:'#fbf1c7', text:'#ebdbb2', subtext1:'#d5c4a1', subtext0:'#bdae93', overlay2:'#928374', overlay1:'#7c6f64', overlay0:'#665c54', surface2:'#665c54', surface1:'#504945', surface0:'#3c3836', base:'#282828', mantle:'#1d2021', crust:'#1b1b1b'}),
    'tokyo-night': theme('Tokyo Night', false, true, '#7aa2f7', '#bb9af7', {red:'#f7768e', maroon:'#ff9e64', peach:'#ff9e64', yellow:'#e0af68', green:'#9ece6a', teal:'#73daca', sky:'#7dcfff', sapphire:'#7aa2f7', blue:'#7aa2f7', mauve:'#bb9af7', pink:'#f7768e', lavender:'#c0caf5', rosewater:'#cfc9c2', flamingo:'#ff9e9e', text:'#c0caf5', subtext1:'#a9b1d6', subtext0:'#9aa5ce', overlay2:'#737aa2', overlay1:'#565f89', overlay0:'#414868', surface2:'#414868', surface1:'#30344a', surface0:'#24283b', base:'#1a1b26', mantle:'#16161e', crust:'#11111a'}),
    'solarized-dark': theme('Solarized Dark', false, true, '#268bd2', '#6c71c4', {red:'#dc322f', maroon:'#cb4b16', peach:'#cb4b16', yellow:'#b58900', green:'#859900', teal:'#2aa198', sky:'#268bd2', sapphire:'#268bd2', blue:'#268bd2', mauve:'#6c71c4', pink:'#d33682', lavender:'#6c71c4', rosewater:'#eee8d5', flamingo:'#fdf6e3', text:'#839496', subtext1:'#93a1a1', subtext0:'#657b83', overlay2:'#586e75', overlay1:'#586e75', overlay0:'#073642', surface2:'#174652', surface1:'#0d3a45', surface0:'#073642', base:'#002b36', mantle:'#00212b', crust:'#001a22'})
  };

  function theme(label, catppuccin, dark, defaultAccent, secondary, colors) {
    colors.label = label;
    colors.catppuccin = catppuccin;
    colors.dark = dark;
    colors.defaultAccent = defaultAccent;
    colors.secondary = secondary;
    return colors;
  }

  function t(key, fallback) {
    const i18n = window.IglooI18n;
    if (i18n && i18n.messages && Object.prototype.hasOwnProperty.call(i18n.messages, key)) {
      return i18n.messages[key];
    }
    return fallback;
  }

  function normalizeHex(value) {
    value = String(value || '').trim().toLowerCase();
    return /^#[0-9a-f]{6}$/.test(value) ? value : '';
  }

  function rgb(hex) {
    hex = normalizeHex(hex) || DEFAULT_ACCENT;
    return {
      r: parseInt(hex.slice(1, 3), 16),
      g: parseInt(hex.slice(3, 5), 16),
      b: parseInt(hex.slice(5, 7), 16)
    };
  }

  function rgbText(c) { return c.r + ', ' + c.g + ', ' + c.b; }
  function hex(c) {
    return '#' + [c.r, c.g, c.b].map(function (v) {
      return Math.max(0, Math.min(255, Math.round(v))).toString(16).padStart(2, '0');
    }).join('');
  }
  function luminance(c) {
    function linear(v) {
      const x = v / 255;
      return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4);
    }
    return 0.2126 * linear(c.r) + 0.7152 * linear(c.g) + 0.0722 * linear(c.b);
  }
  function contrastRatio(a, b) {
    const la = luminance(a), lb = luminance(b);
    const high = Math.max(la, lb), low = Math.min(la, lb);
    return (high + 0.05) / (low + 0.05);
  }
  function themeBackgrounds(themeData) {
    return [themeData.base, themeData.mantle, themeData.surface0];
  }
  function minimumContrastAcross(backgrounds, foreground) {
    let minimum = Infinity;
    const fg = rgb(foreground);
    backgrounds.forEach(function (background) {
      minimum = Math.min(minimum, contrastRatio(rgb(background), fg));
    });
    return minimum;
  }
  function firstReadableColorAcross(backgrounds, candidates, minimumContrast) {
    let best = candidates[candidates.length - 1], bestRatio = 0;
    for (let i = 0; i < candidates.length; i++) {
      const ratio = minimumContrastAcross(backgrounds, candidates[i]);
      if (ratio > bestRatio) {
        best = candidates[i];
        bestRatio = ratio;
      }
      if (ratio >= minimumContrast) return candidates[i].toLowerCase();
    }
    return best.toLowerCase();
  }
  function readableMutedText(themeData) {
    return firstReadableColorAcross(
      themeBackgrounds(themeData),
      [themeData.overlay0, themeData.overlay1, themeData.overlay2, themeData.subtext0, themeData.subtext1],
      3.0
    );
  }
  function readableHandleText(themeData) {
    return firstReadableColorAcross(
      themeBackgrounds(themeData),
      [themeData.overlay0, themeData.subtext0, themeData.subtext1, themeData.text],
      4.5
    );
  }
  function blend(a, b, amount) {
    return {
      r: a.r * (1 - amount) + b.r * amount,
      g: a.g * (1 - amount) + b.g * amount,
      b: a.b * (1 - amount) + b.b * amount
    };
  }
  function onAccent(accent) { return luminance(rgb(accent)) > 0.35 ? '#11111b' : '#ffffff'; }
  function derivedSecondary(id, themeData, accent) {
    if (id === DEFAULT_THEME && normalizeHex(accent) === normalizeHex(DEFAULT_ACCENT)) return '#8b2e2e';
    if (normalizeHex(accent) === normalizeHex(themeData.defaultAccent)) return themeData.secondary.toLowerCase();
    const base = rgb(accent);
    const target = luminance(base) > 0.45 ? rgb(themeData.crust) : rgb(themeData.text);
    return hex(blend(base, target, 0.22));
  }

  function line(name, value) { return '    ' + name + ': ' + value + ';\n'; }
  function rgbLine(name, value) { return line(name, rgbText(rgb(value))); }

  function themeVars(id, themeData, accent, colorScheme) {
    const secondary = derivedSecondary(id, themeData, accent);
    let css = '';
    css += line('color-scheme', colorScheme || (themeData.dark ? 'dark' : 'light'));
    css += line('--web-theme-id', JSON.stringify(id));
    accentNames.forEach(function (entry) { css += line('--ctp-' + entry[0], themeData[entry[0]].toLowerCase()); });
    ['text','subtext1','subtext0','overlay2','overlay1','overlay0','surface2','surface1','surface0','base','mantle','crust'].forEach(function (name) {
      css += line('--ctp-' + name, themeData[name].toLowerCase());
    });
    css += rgbLine('--ctp-red-rgb', themeData.red) + rgbLine('--ctp-maroon-rgb', themeData.maroon) + rgbLine('--ctp-peach-rgb', themeData.peach) + rgbLine('--ctp-yellow-rgb', themeData.yellow) + rgbLine('--ctp-green-rgb', themeData.green) + rgbLine('--ctp-blue-rgb', themeData.blue) + rgbLine('--ctp-lavender-rgb', themeData.lavender) + rgbLine('--ctp-mauve-rgb', themeData.mauve);
    css += rgbLine('--ctp-text-rgb', themeData.text) + rgbLine('--ctp-subtext1-rgb', themeData.subtext1) + rgbLine('--ctp-subtext0-rgb', themeData.subtext0) + rgbLine('--ctp-overlay2-rgb', themeData.overlay2) + rgbLine('--ctp-overlay1-rgb', themeData.overlay1);
    css += rgbLine('--ctp-base-rgb', themeData.base) + rgbLine('--ctp-mantle-rgb', themeData.mantle) + rgbLine('--ctp-crust-rgb', themeData.crust) + rgbLine('--ctp-surface0-rgb', themeData.surface0) + rgbLine('--ctp-surface1-rgb', themeData.surface1) + rgbLine('--ctp-overlay0-rgb', themeData.overlay0);
    css += line('--bg-primary', themeData.base) + line('--bg-secondary', themeData.surface0) + line('--bg-tertiary', themeData.crust);
    css += line('--bg-hover', 'rgba(var(--text-primary-rgb), 0.06)') + line('--surface-hover', 'var(--bg-hover)') + line('--bg-glass', 'rgba(var(--bg-primary-rgb), 0.92)');
    css += rgbLine('--bg-primary-rgb', themeData.base) + rgbLine('--bg-secondary-rgb', themeData.surface0) + rgbLine('--bg-tertiary-rgb', themeData.crust);
    const mutedText = readableMutedText(themeData);
    css += line('--text-primary', themeData.text) + line('--text-secondary', themeData.subtext1) + line('--text-muted', mutedText);
    css += line('--text-handle', readableHandleText(themeData));
    css += rgbLine('--text-primary-rgb', themeData.text) + rgbLine('--text-secondary-rgb', themeData.subtext1) + rgbLine('--text-muted-rgb', mutedText) + rgbLine('--text-handle-rgb', readableHandleText(themeData));
    css += line('--accent-primary', accent) + line('--accent-secondary', secondary);
    css += line('--accent-primary-rgb', rgbText(rgb(accent))) + line('--accent-secondary-rgb', rgbText(rgb(secondary)));
    css += line('--accent-gradient', 'linear-gradient(135deg, ' + accent + ' 0%, ' + secondary + ' 100%)');
    css += line('--accent-yellow', themeData.yellow) + line('--accent-green', themeData.green);
    css += line('--success', themeData.green) + line('--warning', themeData.yellow) + line('--error', themeData.red);
    css += rgbLine('--status-success-rgb', themeData.green) + rgbLine('--status-warning-rgb', themeData.yellow) + rgbLine('--status-error-rgb', themeData.red);
    css += line('--border-color', 'rgba(var(--text-primary-rgb), 0.06)') + line('--shadow', themeData.dark ? '0 14px 38px rgba(0, 0, 0, 0.5)' : '0 14px 38px rgba(31, 35, 40, 0.16)');
    css += line('--surface-soft', themeData.base) + line('--surface-strong', themeData.crust);
    css += rgbLine('--surface-soft-rgb', themeData.mantle) + rgbLine('--surface-strong-rgb', themeData.crust) + rgbLine('--surface-elevated-rgb', themeData.surface1);
    css += line('--status-info', themeData.blue) + line('--status-success', themeData.green) + line('--status-warning', themeData.yellow) + line('--status-error', themeData.red) + line('--status-purple', themeData.lavender) + line('--status-source', themeData.mauve) + line('--status-tag', themeData.sky) + line('--status-muted', mutedText);
    css += line('--media-primary-color', themeData.text) + line('--media-range-bar-color', 'rgba(var(--accent-primary-rgb), 0.5)') + line('--media-icon-color', themeData.text);
    css += line('--on-accent', onAccent(accent));
    return css;
  }

  function buildCSS(settings) {
    const id = themes[settings.themeId] ? settings.themeId : DEFAULT_THEME;
    const themeData = themes[id];
    const accent = normalizeHex(settings.accent) || themeData.defaultAccent.toLowerCase();
    let css = '/* Igloo web theme preview. */\n:root {\n';
    if (id === 'system') {
      css += themeVars('system', themes['catppuccin-mocha'], accent, 'light dark');
      css += '}\n\n@media (prefers-color-scheme: light) {\n:root {\n';
      css += themeVars('system', themes['catppuccin-latte'], accent, 'light dark');
      css += '}\n}\n';
    } else {
      css += themeVars(id, themeData, accent);
      css += '}\n';
    }
    if (settings.customCSS && settings.customCSS.trim()) css += '\n/* Custom web theme CSS. */\n' + settings.customCSS + '\n';
    return css;
  }

  function formSettings(form) {
    const themeSelect = form.querySelector('[name=web_theme_id]');
    const accentInput = form.querySelector('[name=web_theme_accent]');
    const customInput = form.querySelector('[name=web_custom_css]');
    return {
      themeId: themeSelect ? themeSelect.value : DEFAULT_THEME,
      accent: accentInput ? accentInput.value : DEFAULT_ACCENT,
      customCSS: customInput ? customInput.value.slice(0, MAX_CUSTOM_CSS_BYTES) : ''
    };
  }

  function previewEl() {
    let el = doc.getElementById('igloo-theme-preview');
    if (!el) {
      el = doc.createElement('style');
      el.id = 'igloo-theme-preview';
      doc.head.appendChild(el);
    }
    return el;
  }

  function removePreview() {
    const el = doc.getElementById('igloo-theme-preview');
    if (el) el.remove();
  }

  function setAccentError(form, message) {
    const error = form.querySelector('[data-web-theme-accent-error]');
    const input = form.querySelector('[name=web_theme_accent]');
    if (error) error.textContent = message || '';
    if (input && input.setCustomValidity) input.setCustomValidity(message || '');
  }

  function validate(form) {
    const input = form.querySelector('[name=web_theme_accent]');
    const picker = form.querySelector('[data-web-theme-accent-picker]');
    const value = input ? input.value : DEFAULT_ACCENT;
    const normalized = normalizeHex(value);
    if (!normalized) {
      setAccentError(form, t('settings_theme_accent_invalid', 'Use #rrggbb'));
      return null;
    }
    setAccentError(form, '');
    if (input && input.value !== normalized) input.value = normalized;
    if (picker && picker.value !== normalized) picker.value = normalized;
    return formSettings(form);
  }

  function applyPreview(form) {
    const settings = validate(form);
    if (!settings) {
      removePreview();
      return false;
    }
    previewEl().textContent = buildCSS(settings);
    return true;
  }

  function updatePills(form) {
    const select = form.querySelector('[name=web_theme_id]');
    const row = form.querySelector('[data-catppuccin-accent-row]');
    const pills = form.querySelector('[data-catppuccin-accent-pills]');
    if (!select || !row || !pills) return;
    const themeData = themes[select.value] || themes[DEFAULT_THEME];
    if (!themeData.catppuccin) {
      row.style.display = 'none';
      pills.innerHTML = '';
      return;
    }
    row.style.display = '';
    pills.innerHTML = accentNames.map(function (entry) {
      const id = entry[0], label = entry[1], hexValue = themeData[id];
      return '<button type="button" class="theme-accent-pill" data-catppuccin-accent="' + id + '" data-accent-hex="' + hexValue + '" aria-label="' + label + '" title="' + label + '" style="--swatch:' + hexValue + ';"><span class="theme-accent-pill-swatch"></span><span>' + label + '</span></button>';
    }).join('');
  }

  function setAccent(form, value) {
    const normalized = normalizeHex(value) || value;
    const picker = form.querySelector('[data-web-theme-accent-picker]');
    const input = form.querySelector('[name=web_theme_accent]');
    if (input) input.value = normalized;
    if (picker && normalizeHex(normalized)) picker.value = normalized;
  }

  function init(root) {
    root = root || doc;
    const form = root.id === 'prefs-form' ? root : root.querySelector && root.querySelector('#prefs-form');
    if (!form || form.dataset.webThemeReady === '1') return;
    form.dataset.webThemeReady = '1';
    const select = form.querySelector('[name=web_theme_id]');
    const input = form.querySelector('[name=web_theme_accent]');
    const picker = form.querySelector('[data-web-theme-accent-picker]');
    const custom = form.querySelector('[name=web_custom_css]');

    if (select) {
      select.addEventListener('change', function () {
        const themeData = themes[select.value] || themes[DEFAULT_THEME];
        setAccent(form, themeData.defaultAccent);
        updatePills(form);
        applyPreview(form);
      });
    }
    if (picker) picker.addEventListener('input', function () { setAccent(form, picker.value); applyPreview(form); });
    if (input) input.addEventListener('input', function () { applyPreview(form); });
    if (custom) custom.addEventListener('input', function () { applyPreview(form); });
    form.addEventListener('click', function (event) {
      const pill = event.target && event.target.closest ? event.target.closest('[data-accent-hex]') : null;
      if (!pill || !form.contains(pill)) return;
      setAccent(form, pill.getAttribute('data-accent-hex') || DEFAULT_ACCENT);
      applyPreview(form);
    });
    form.addEventListener('submit', function (event) {
      if (!validate(form)) {
        event.preventDefault();
        event.stopPropagation();
      }
    }, true);
    updatePills(form);
  }

  function commitAfterSave() {
    removePreview();
    const link = doc.getElementById('igloo-theme-css');
    if (link) link.href = '/api/theme.css?v=' + Date.now();
  }

  window.IglooWebTheme = {
    init: init,
    revertPreview: removePreview,
    commitAfterSave: commitAfterSave,
    _buildCSS: buildCSS
  };

  doc.addEventListener('DOMContentLoaded', function () { init(doc); });
  doc.addEventListener('htmx:afterSettle', function (event) {
    const elt = event.detail && event.detail.elt;
    init(elt && elt.parentElement ? elt.parentElement : doc);
  });
})();
