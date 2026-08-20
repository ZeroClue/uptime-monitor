(function () {
    'use strict';

    const THEME_KEY = 'uptime-monitor-theme';

    function cssVar(name) {
        return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
    }

    function applyTheme(theme) {
        const root = document.documentElement;
        root.setAttribute('data-theme', theme);
        const meta = document.getElementById('theme-color-meta');
        if (meta) {
            const bg = cssVar('--bg');
            if (bg) meta.setAttribute('content', bg);
        }
        const toggle = document.getElementById('theme-toggle');
        if (toggle) {
            toggle.textContent = theme === 'dark' ? '☾' : '☀';
            toggle.setAttribute('aria-pressed', String(theme === 'dark'));
        }
        document.dispatchEvent(new CustomEvent('themechange', { detail: { theme: theme } }));
    }

    function currentTheme() {
        return document.documentElement.getAttribute('data-theme') || 'dark';
    }

    function initTheme() {
        let theme = localStorage.getItem(THEME_KEY);
        if (!theme) {
            // default dark per design; OS preference only applies after a manual toggle
            theme = 'dark';
        }
        applyTheme(theme);
    }

    function toggleTheme() {
        const next = currentTheme() === 'dark' ? 'light' : 'dark';
        localStorage.setItem(THEME_KEY, next);
        applyTheme(next);
    }

    function initNav() {
        const path = window.location.pathname;
        const nav = document.querySelector('.nav-links');
        if (!nav) return;
        let activeKey = null;
        if (path === '/' || path.startsWith('/host/')) activeKey = 'hosts';
        else if (path.startsWith('/compare')) activeKey = 'compare';
        else if (path.startsWith('/projects')) activeKey = 'projects';
        else if (path.startsWith('/alerts')) activeKey = 'alerts';
        else if (path.startsWith('/monitor')) activeKey = 'monitor';
        nav.querySelectorAll('a[data-nav]').forEach(a => {
            if (a.getAttribute('data-nav') === activeKey) a.classList.add('active');
        });
    }

    // ---- Chart.js theme-aware global defaults ----
    function chartDefaults() {
        if (typeof Chart === 'undefined') return;
        const dim = cssVar('--text-dim');
        const border = cssVar('--border');
        Chart.defaults.font.family = cssVar('--mono');
        Chart.defaults.color = dim;
        Chart.defaults.borderColor = border;
        Chart.defaults.plugins.legend.labels.boxWidth = 12;
        Chart.defaults.plugins.legend.labels.font = { size: 11 };
    }

    document.addEventListener('DOMContentLoaded', function () {
        initTheme();
        initNav();
        chartDefaults();
        const toggle = document.getElementById('theme-toggle');
        if (toggle) {
            toggle.addEventListener('click', toggleTheme);
            toggle.addEventListener('keydown', function (e) {
                if (e.key === ' ' || e.key === 'Enter') {
                    e.preventDefault();
                    toggleTheme();
                }
            });
        }
        // restyle any existing charts on theme change
        document.addEventListener('themechange', function () {
            chartDefaults();
            if (typeof Chart !== 'undefined') {
                Object.values(Chart.instances || {}).forEach(function (instance) {
                    instance.options.scales.x.ticks.color = cssVar('--text-dim');
                    instance.options.scales.y.ticks.color = cssVar('--text-dim');
                    instance.options.scales.x.grid.color = cssVar('--border');
                    instance.options.scales.y.grid.color = cssVar('--border');
                    // re-read series palette so dataset colors follow the new theme
                    const colors = [
                        cssVar('--series-1'), cssVar('--series-2'), cssVar('--series-3'),
                        cssVar('--series-4'), cssVar('--series-5'), cssVar('--series-6'),
                        cssVar('--accent'), cssVar('--ok'), cssVar('--warn'), cssVar('--crit')
                    ].filter(Boolean);
                    (instance.data.datasets || []).forEach(function (ds, i) {
                        if (colors.length) {
                            ds.borderColor = colors[i % colors.length];
                            ds.backgroundColor = colors[i % colors.length] + '22';
                        }
                    });
                    instance.update();
                });
            }
        });
    });
})();