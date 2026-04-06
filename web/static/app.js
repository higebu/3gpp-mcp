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
})();
