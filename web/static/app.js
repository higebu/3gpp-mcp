// Dark mode toggle
(function () {
    const toggle = document.getElementById('theme-toggle');
    const html = document.documentElement;

    // Initialize theme
    const saved = localStorage.getItem('theme');
    if (saved) {
        html.dataset.theme = saved;
    } else if (window.matchMedia('(prefers-color-scheme: dark)').matches) {
        html.dataset.theme = 'dark';
    }

    if (toggle) {
        toggle.addEventListener('click', function () {
            const next = html.dataset.theme === 'dark' ? 'light' : 'dark';
            html.dataset.theme = next;
            localStorage.setItem('theme', next);
        });
    }

    // TOC toggle for mobile
    const tocToggle = document.getElementById('toc-toggle');
    const tocSidebar = document.getElementById('toc-sidebar');

    if (tocToggle && tocSidebar) {
        tocToggle.addEventListener('click', function () {
            tocSidebar.classList.toggle('open');
        });

        // Close TOC when clicking a link (mobile)
        tocSidebar.querySelectorAll('a').forEach(function (link) {
            link.addEventListener('click', function () {
                tocSidebar.classList.remove('open');
            });
        });
    }

    // Scroll TOC to active item
    if (tocSidebar) {
        const activeItem = tocSidebar.querySelector('.toc-item.active');
        if (activeItem) {
            const sidebarRect = tocSidebar.getBoundingClientRect();
            const itemRect = activeItem.getBoundingClientRect();
            const offset = itemRect.top - sidebarRect.top - sidebarRect.height / 2 + itemRect.height / 2;
            tocSidebar.scrollTop += offset;
        }
    }

    // Prev/Next chapter keyboard navigation (Left/Right arrow keys). Ignored
    // while the user is operating a focused interactive control (text
    // inputs, the series <select>, buttons, ARIA widgets) or holding a
    // modifier key, so it doesn't clash with that control's own Left/Right
    // behavior or browser/OS shortcuts.
    document.addEventListener('keydown', function (e) {
        if (e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) {
            return;
        }
        var active = document.activeElement;
        if (active && (active.isContentEditable || active.matches('input, textarea, select, button, [contenteditable], [role="button"], [role="textbox"], [role="combobox"], [role="listbox"]'))) {
            return;
        }
        var selector = e.key === 'ArrowLeft' ? '.section-nav-prev' : e.key === 'ArrowRight' ? '.section-nav-next' : null;
        if (!selector) {
            return;
        }
        var link = document.querySelector(selector);
        if (link) {
            window.location.href = link.getAttribute('href');
        }
    });

    // Render LaTeX math emitted by the DOCX converter. The server wraps each
    // equation in a <span class="math-inline|math-display"> whose text content
    // is the raw LaTeX; KaTeX renders it in place.
    if (window.katex) {
        document.querySelectorAll('.math-inline, .math-display').forEach(function (el) {
            try {
                katex.render(el.textContent, el, {
                    displayMode: el.classList.contains('math-display'),
                    throwOnError: false,
                });
            } catch (e) {
                // Leave the raw LaTeX visible if rendering fails.
            }
        });
    }
})();
