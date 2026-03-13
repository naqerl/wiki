/**
 * Theme Switcher - Dynamic theme loading and switching
 * Persists preference in localStorage, respects browser preference
 */

(function() {
    'use strict';

    // Theme configuration - 4 themes: default light/dark + sepia + retro
    const THEMES = [
        { id: 'default-light', name: 'Default Light', file: 'theme-default-light.css' },
        { id: 'default-dark', name: 'Default Dark', file: 'theme-default-dark.css' },
        { id: 'sepia', name: 'Sepia', file: 'theme-sepia.css' },
        { id: 'retro', name: 'Retro Terminal', file: 'theme-retro.css' }
    ];

    const STORAGE_KEY = 'wiki-theme';
    const THEME_CSS_ID = 'wiki-theme-css';

    /**
     * Get system preference (dark/light)
     */
    function getSystemPreference() {
        if (window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches) {
            return 'default-dark';
        }
        return 'default-light';
    }

    /**
     * Get the stored theme or default based on system preference
     */
    function getStoredTheme() {
        try {
            const stored = localStorage.getItem(STORAGE_KEY);
            if (stored) {
                const theme = THEMES.find(t => t.id === stored);
                if (theme) return theme;
            }
        } catch (e) {
            // localStorage not available
        }
        // Default to system preference
        const sysPref = getSystemPreference();
        return THEMES.find(t => t.id === sysPref) || THEMES[0];
    }

    /**
     * Store theme preference
     */
    function storeTheme(themeId) {
        try {
            localStorage.setItem(STORAGE_KEY, themeId);
        } catch (e) {
            // localStorage not available
        }
    }

    /**
     * Apply a theme
     */
    function applyTheme(theme) {
        // Remove existing theme CSS
        const existing = document.getElementById(THEME_CSS_ID);
        if (existing) {
            existing.remove();
        }

        // Create new link element
        const link = document.createElement('link');
        link.id = THEME_CSS_ID;
        link.rel = 'stylesheet';
        link.href = '/static/css/' + theme.file;

        // Add to head
        const head = document.head || document.getElementsByTagName('head')[0];
        head.appendChild(link);

        // Set data attribute for CSS hooks
        document.documentElement.setAttribute('data-theme', theme.id);

        // Store preference
        storeTheme(theme.id);
    }

    /**
     * Create theme selector dropdown
     */
    function createThemeSelector() {
        const container = document.createElement('div');
        container.className = 'theme-selector';

        const select = document.createElement('select');
        select.id = 'theme-select';
        select.setAttribute('aria-label', 'Select theme');

        THEMES.forEach(theme => {
            const option = document.createElement('option');
            option.value = theme.id;
            option.textContent = theme.name;
            select.appendChild(option);
        });

        // Set current selection
        const current = getStoredTheme();
        select.value = current.id;

        // Handle change
        select.addEventListener('change', function() {
            const theme = THEMES.find(t => t.id === this.value);
            if (theme) {
                applyTheme(theme);
            }
        });

        container.appendChild(select);

        return container;
    }

    /**
     * Check if we're on the index page
     */
    function isIndexPage() {
        const path = window.location.pathname;
        return path === '/' || path === '';
    }

    /**
     * Initialize theme system
     */
    function init() {
        // Apply stored theme (or system preference)
        const theme = getStoredTheme();
        applyTheme(theme);

        // Only show theme selector on index page
        if (isIndexPage()) {
            const utilityBar = document.querySelector('.utility-bar');
            if (utilityBar) {
                const selector = createThemeSelector();
                utilityBar.appendChild(selector);
            }
        }

        // Listen for system preference changes
        if (window.matchMedia) {
            const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');
            mediaQuery.addEventListener('change', function(e) {
                // Only auto-switch if user hasn't manually set a preference
                try {
                    const stored = localStorage.getItem(STORAGE_KEY);
                    if (!stored) {
                        const newThemeId = e.matches ? 'default-dark' : 'default-light';
                        const newTheme = THEMES.find(t => t.id === newThemeId);
                        if (newTheme) {
                            applyTheme(newTheme);
                            // Update selector if it exists
                            const select = document.getElementById('theme-select');
                            if (select) {
                                select.value = newThemeId;
                            }
                        }
                    }
                } catch (e) {
                    // localStorage not available
                }
            });
        }
    }

    // Initialize when DOM is ready
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
