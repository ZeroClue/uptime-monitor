(function () {
    'use strict';

    const THEME_KEY = 'uptime-monitor-theme';

    function applyTheme(theme) {
        const root = document.documentElement;
        root.setAttribute('data-theme', theme);
        const meta = document.getElementById('theme-color-meta');
        if (meta) {
            const bg = getComputedStyle(root).getPropertyValue('--bg').trim();
            if (bg) meta.setAttribute('content', bg);
        }
        const toggle = document.getElementById('theme-toggle');
        if (toggle) {
            toggle.textContent = theme === 'dark' ? '☾' : '☀';
            toggle.setAttribute('aria-pressed', String(theme === 'dark'));
        }
    }

    function currentTheme() {
        return document.documentElement.getAttribute('data-theme') || 'dark';
    }

    function initTheme() {
        let theme = localStorage.getItem(THEME_KEY);
        if (!theme) {
            theme = window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
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

    document.addEventListener('DOMContentLoaded', function () {
        initTheme();
        initNav();
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
    });
})();