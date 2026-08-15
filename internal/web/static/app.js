// Dark mode toggle
(function () {
    // The theme itself is initialized by an inline script in the <head> of
    // layout.html, before the first paint; this only wires up the toggle.
    const toggle = document.getElementById('theme-toggle');
    const html = document.documentElement;

    // localStorage can throw (private browsing, storage disabled); the
    // preference then simply lasts for the page instead of persisting.
    const persist = function (key, value) {
        try {
            localStorage.setItem(key, value);
        } catch (e) { /* ignore */ }
    };

    if (toggle) {
        toggle.addEventListener('click', function () {
            const next = html.dataset.theme === 'dark' ? 'light' : 'dark';
            html.dataset.theme = next;
            persist('theme', next);
        });
    }

    // Settings popover: code highlighting theme. The choice sets
    // data-code-theme on <html> (selecting a light/dark CSS pair in
    // style.css) and persists in localStorage; the inline script in
    // layout.html applies it before first paint on later page loads.
    const settingsToggle = document.getElementById('settings-toggle');
    const settingsPopover = document.getElementById('settings-popover');

    if (settingsToggle && settingsPopover) {
        // Keep the popover inside the viewport: on narrow screens the
        // navbar wraps and the gear no longer sits at the right edge, so
        // right-aligning the popover to it can push it off the left edge.
        // Measure and shift right just enough, without crossing the right
        // edge either. Recomputed on resize (e.g. orientation change) while
        // the popover is open, since the offset depends on the layout.
        const clampToViewport = function () {
            settingsPopover.style.right = '';
            const margin = 8;
            const rect = settingsPopover.getBoundingClientRect();
            if (rect.left < margin) {
                const shift = Math.min(margin - rect.left, window.innerWidth - margin - rect.right);
                if (shift > 0) {
                    settingsPopover.style.right = -shift + 'px';
                }
            }
        };
        const setOpen = function (open) {
            settingsPopover.hidden = !open;
            settingsToggle.setAttribute('aria-expanded', String(open));
            if (open) {
                clampToViewport();
            }
        };
        window.addEventListener('resize', function () {
            if (!settingsPopover.hidden) {
                clampToViewport();
            }
        });
        settingsToggle.addEventListener('click', function () {
            setOpen(settingsPopover.hidden);
        });
        document.addEventListener('click', function (e) {
            if (!settingsPopover.hidden && !settingsPopover.contains(e.target) && !settingsToggle.contains(e.target)) {
                setOpen(false);
            }
        });
        document.addEventListener('keydown', function (e) {
            if (e.key === 'Escape' && !settingsPopover.hidden) {
                setOpen(false);
                settingsToggle.focus();
            }
        });

        const current = html.dataset.codeTheme || 'catppuccin';
        settingsPopover.querySelectorAll('input[name="code-theme"]').forEach(function (radio) {
            radio.checked = radio.value === current;
            radio.addEventListener('change', function () {
                if (radio.checked) {
                    html.dataset.codeTheme = radio.value;
                    persist('codeTheme', radio.value);
                }
            });
        });
    }

    // TOC toggle for mobile
    const tocToggle = document.getElementById('toc-toggle');
    const tocSidebar = document.getElementById('toc-sidebar');

    if (tocToggle && tocSidebar) {
        tocToggle.addEventListener('click', function () {
            tocSidebar.classList.toggle('open');
        });

        const tocClose = document.getElementById('toc-close');
        if (tocClose) {
            tocClose.addEventListener('click', function () {
                tocSidebar.classList.remove('open');
            });
        }

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
        if (document.querySelector('dialog.lightbox[open]')) {
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

    // Image lightbox: figures are rendered at the document's display size,
    // which is often smaller than the viewport, so clicking one opens a
    // modal <dialog> scaled to fit the screen. The dialog is created lazily
    // on first use and reused; a click anywhere closes it (clicks on the
    // ::backdrop target the dialog itself), and Escape is native <dialog>
    // behavior.
    const sectionBody = document.querySelector('.section-body');
    if (sectionBody && typeof HTMLDialogElement !== 'undefined' && HTMLDialogElement.prototype.showModal) {
        let lightbox = null;
        const openLightbox = function (img) {
            if (!lightbox) {
                lightbox = document.createElement('dialog');
                lightbox.className = 'lightbox';
                lightbox.appendChild(document.createElement('img'));
                lightbox.addEventListener('click', function () {
                    lightbox.close();
                });
                document.body.appendChild(lightbox);
            }
            const large = lightbox.querySelector('img');
            large.src = img.currentSrc || img.src;
            large.alt = img.alt;
            lightbox.setAttribute('aria-label', img.alt || 'Figure');
            lightbox.showModal();
        };
        // Figures are focusable buttons so keyboard users can open the
        // lightbox too; closing a <dialog> restores focus to the figure.
        sectionBody.querySelectorAll('img').forEach(function (img) {
            img.tabIndex = 0;
            img.setAttribute('role', 'button');
        });
        sectionBody.addEventListener('click', function (e) {
            const img = e.target.closest('img');
            if (img) {
                openLightbox(img);
            }
        });
        sectionBody.addEventListener('keydown', function (e) {
            if ((e.key === 'Enter' || e.key === ' ') && e.target.matches('img')) {
                e.preventDefault();
                openLightbox(e.target);
            }
        });
    }

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
