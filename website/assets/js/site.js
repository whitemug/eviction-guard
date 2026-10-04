(function () {
  var root = document.documentElement;
  var themeButton = document.querySelector("[data-theme-toggle]");
  if (themeButton) {
    themeButton.addEventListener("click", function () {
      var next = root.getAttribute("data-theme") === "dark" ? "light" : "dark";
      root.setAttribute("data-theme", next);
      try { localStorage.setItem("theme", next); } catch (e) {}
    });
  }

  document.addEventListener("click", function (event) {
    var button = event.target.closest("[data-copy]");
    if (!button) return;
    var block = button.closest(".codeblock");
    var code = block && block.querySelector("code");
    if (!code) return;
    var label = button.textContent;
    navigator.clipboard.writeText(code.innerText).then(function () {
      button.textContent = "Copied";
      setTimeout(function () { button.textContent = label; }, 1200);
    });
  });

  var dialog = document.getElementById("search-dialog");
  var input = document.getElementById("search-input");
  var results = document.getElementById("search-results");
  var status = document.getElementById("search-status");
  var openButton = document.querySelector("[data-search-open]");
  var pagefindPromise = null;
  var timer = 0;

  function loadPagefind() {
    if (!pagefindPromise) {
      var src = document.body.dataset.pagefind;
      pagefindPromise = import(src).then(function (mod) {
        if (mod.init) return mod.init().then(function () { return mod; });
        return mod;
      });
    }
    return pagefindPromise;
  }

  function openSearch() {
    if (!dialog || !input) return;
    if (typeof dialog.showModal === "function") dialog.showModal();
    input.focus();
    loadPagefind().catch(function () {
      status.textContent = "Search ships with the published site. This preview has no index yet.";
    });
  }

  if (openButton) openButton.addEventListener("click", openSearch);

  document.addEventListener("keydown", function (event) {
    var tag = event.target && event.target.tagName;
    if (event.key === "/" && tag !== "INPUT" && tag !== "TEXTAREA" && !event.metaKey && !event.ctrlKey) {
      event.preventDefault();
      openSearch();
    }
  });

  function render(items) {
    results.innerHTML = "";
    if (!items.length) {
      status.textContent = "No matches.";
      return;
    }
    status.textContent = "";
    items.forEach(function (item) {
      var li = document.createElement("li");
      var a = document.createElement("a");
      a.href = item.url;
      var title = document.createElement("strong");
      title.textContent = (item.meta && item.meta.title) || item.url;
      var excerpt = document.createElement("p");
      excerpt.innerHTML = item.excerpt || "";
      a.appendChild(title);
      a.appendChild(excerpt);
      a.addEventListener("click", function () { dialog.close(); });
      li.appendChild(a);
      results.appendChild(li);
    });
  }

  if (input) {
    input.addEventListener("input", function () {
      var query = input.value.trim();
      clearTimeout(timer);
      if (!query) {
        results.innerHTML = "";
        status.textContent = "";
        return;
      }
      timer = setTimeout(function () {
        loadPagefind().then(function (pf) {
          return pf.search(query);
        }).then(function (search) {
          return Promise.all(search.results.slice(0, 8).map(function (result) {
            return result.data();
          }));
        }).then(render).catch(function () {
          status.textContent = "Search ships with the published site. This preview has no index yet.";
        });
      }, 120);
    });
  }
})();
