(function () {
  var urlInput = document.getElementById("url");
  var versionInput = document.getElementById("version");
  var autoUpdateInput = document.getElementById("autoUpdateHours");
  var autoStartCoreInput = document.getElementById("autoStartCore");
  var startMinimizedTrayInput = document.getElementById("startMinimizedTray");
  var profileWrap = document.getElementById("profileWrap");
  var profileNameInput = document.getElementById("profileNameInput");
  var profilePicker = document.getElementById("profilePicker");
  var profileValueNode = document.getElementById("profileValue");
  var profileMenu = document.getElementById("profileMenu");
  var newProfileBtn = document.getElementById("newProfile");
  var deleteProfileBtn = document.getElementById("deleteProfile");
  var checkConfigBtn = document.getElementById("checkConfig");
  var startStopBtn = document.getElementById("startStop");
  var copyLogsBtn = document.getElementById("copyLogs");
  var mobileActionsWrap = document.getElementById("mobileActionsWrap");
  var mobileActionsToggleBtn = document.getElementById("mobileActionsToggle");
  var mobileActionsMenu = document.getElementById("mobileActionsMenu");
  var mobileActionCheckConfigBtn = document.getElementById("mobileActionCheckConfig");
  var mobileActionCopyLogsBtn = document.getElementById("mobileActionCopyLogs");
  var toastStack = document.getElementById("toastStack");
  var statusNode = document.getElementById("status");
  var uptimeNode = document.getElementById("uptime");
  var logsNode = document.getElementById("logs");
  var logsFilterInput = document.getElementById("logsFilter");
  var settingsTitleNode = document.getElementById("settingsTitle");
  var logsTitleNode = document.getElementById("logsTitle");
  var releaseMenuWrap = document.getElementById("releaseMenuWrap");
  var releaseMenuToggleBtn = document.getElementById("releaseMenuToggle");
  var releaseMenuToggleArrowBtn = document.getElementById("releaseMenuToggleArrow");
  var releaseMenuLabelNode = document.getElementById("releaseMenuLabel");
  var releaseMenuNode = document.getElementById("releaseMenu");
  var releaseCurrentCaptionNode = document.getElementById("releaseCurrentCaption");
  var releaseCurrentLinkNode = document.getElementById("releaseCurrentLink");
  var releaseLatestRowNode = document.getElementById("releaseLatestRow");
  var releaseLatestCaptionNode = document.getElementById("releaseLatestCaption");
  var releaseLatestLinkNode = document.getElementById("releaseLatestLink");
  var updateAppBtn = document.getElementById("updateAppBtn");
  var labelUrlNode = document.getElementById("labelUrl");
  var labelVersionNode = document.getElementById("labelVersion");
  var labelAutoUpdateNode = document.getElementById("labelAutoUpdate");
  var labelAutoStartCoreNode = document.getElementById("labelAutoStartCore");
  var labelStartMinimizedTrayNode = document.getElementById("labelStartMinimizedTray");
  var labelProfileNode = document.getElementById("labelProfile");
  var labelRunCheckNode = document.getElementById("labelRunCheck");
  var langRuBtn = document.getElementById("langRu");
  var langEnBtn = document.getElementById("langEn");
  var confirmModal = document.getElementById("confirmModal");
  var confirmModalOverlay = document.getElementById("confirmModalOverlay");
  var confirmTitleNode = document.getElementById("confirmTitle");
  var confirmMessageNode = document.getElementById("confirmMessage");
  var confirmCancelBtn = document.getElementById("confirmCancel");
  var confirmOkBtn = document.getElementById("confirmOk");

  var lastLogId = 0;
  var stateTimer = null;
  var logsTimer = null;
  var uptimeTimer = null;
  var saveTimer = null;
  var stateReqInFlight = false;
  var stateReqQueued = false;
  var logsReqInFlight = false;
  var copyLogsInFlight = false;
  var startupPatchInFlight = false;
  var startupPatchQueued = false;
  var profileRenameTimer = null;
  var profileRenameInFlight = false;
  var profileRenameQueued = false;
  var loadingState = false;
  var logsFilterTimer = null;
  var ansiCodeRegex = /\x1b\[([0-9;]*)m/g;
  var logBuffer = [];
  var logsFilterRegex = null;
  var logsHighlightRegex = null;
  var logsFilterError = "";
  var profileNames = [];
  var selectedProfile = "";
  var profileMenuOpened = false;
  var releaseMenuOpened = false;
  var mobileActionsOpened = false;
  var currentLanguage = "ru";
  var lastRunning = false;
  var lastBusy = false;
  var appUpdateInFlight = false;
  var lastProtoWarn = "";
  var lastAutoUpdateHours = 12;
  var lastAutoStartCore = false;
  var lastStartMinimizedTray = false;
  var lastUptimeSeconds = 0;
  var lastAppReleaseTag = "";
  var lastAppReleaseURL = "";
  var lastAppUpdateAvailable = false;
  var lastAppLatestReleaseTag = "";
  var lastAppLatestReleaseURL = "";
  var lastAppliedUIScale = null;
  var lastVisibilitySyncAt = 0;
  var initialStateRendered = false;
  var confirmAction = null;
  var pollingActive = false;
  var statePollDelay = 0;
  var logsPollDelay = 0;
  var STATE_POLL_IDLE_MS = 4500;
  var STATE_POLL_RUNNING_MS = 2200;
  var STATE_POLL_BUSY_MS = 1200;
  var LOGS_POLL_MIN_MS = 600;
  var LOGS_POLL_MAX_MS = 3200;
  var LOGS_POLL_EMPTY_STEP_MS = 300;
  var LOGS_POLL_ERROR_MS = 4200;
  var MAX_RENDERED_LOG_LINES = 2000;
  var MAX_FILTER_PATTERN_LEN = 256;
  var ANSI_ESC_RAW_MARKER = "\x1b[";
  var ANSI_ESC_FALLBACK_MARKER = "\u2190[";

  var I18N = {
    ru: {
      settings: "Настройки",
      logs: "Логи",
      configUrl: "Ссылка:",
      version: "Версия ядра:",
      autoUpdate: "Автообновление (часы):",
      autoStartCore: "Автозапуск ядра",
      startMinimizedTray: "Запуск в трее",
      profile: "Профиль:",
      runCheck: "Запуск/проверка:",
      checkConfig: "Проверить",
      newProfile: "Новый",
      deleteProfile: "Удалить",
      start: "Старт",
      stop: "Стоп",
      copyLogs: "Копировать логи",
      logsFilterPlaceholder: "Фильтр RegExp",
      logsFilterInvalid: "Некорректный RegExp",
      logsFilterTooLong: "Слишком длинный RegExp",
      actionsMenu: "Действия",
      statusBusy: "Выполняется операция...",
      statusConfigOk: "Конфигурация валидна",
      uptime: "Аптайм",
      statusLogsCopied: "Логи скопированы в буфер обмена",
      releaseButton: "Релиз",
      releaseCurrent: "Текущий релиз",
      releaseLatest: "Новый релиз",
      releaseUnknown: "Недоступно",
      updateApp: "Обновить приложение",
      statusUpdateStarted: "Обновление приложения запущено. Окно будет перезапущено.",
      confirmDelete: "Удалить текущий профиль?",
      confirmTitle: "Подтверждение",
      cancel: "Отмена",
      deleteAction: "Удалить",
      warnPrefix: "WARN: ",
      errorPrefix: "ERROR: "
    },
    en: {
      settings: "Settings",
      logs: "Logs",
      configUrl: "Config URL:",
      version: "Core version:",
      autoUpdate: "Auto-update (hours):",
      autoStartCore: "Auto start core",
      startMinimizedTray: "Start in tray",
      profile: "Profile:",
      runCheck: "Run/Check:",
      checkConfig: "Check",
      newProfile: "New",
      deleteProfile: "Delete",
      start: "Start",
      stop: "Stop",
      copyLogs: "Copy Logs",
      logsFilterPlaceholder: "RegExp filter",
      logsFilterInvalid: "Invalid RegExp",
      logsFilterTooLong: "RegExp is too long",
      actionsMenu: "Actions",
      statusBusy: "Operation in progress...",
      statusConfigOk: "Configuration is valid",
      uptime: "Uptime",
      statusLogsCopied: "Logs copied to clipboard",
      releaseButton: "Release",
      releaseCurrent: "Current release",
      releaseLatest: "New release",
      releaseUnknown: "Unavailable",
      updateApp: "Update app",
      statusUpdateStarted: "Application update started. The app will restart shortly.",
      confirmDelete: "Delete current profile?",
      confirmTitle: "Confirmation",
      cancel: "Cancel",
      deleteAction: "Delete",
      warnPrefix: "WARN: ",
      errorPrefix: "ERROR: "
    }
  };

  function normalizeBridgeError(err) {
    if (!err) return "request failed";
    if (typeof err === "string") {
      return err;
    }
    if (err && typeof err.message === "string" && err.message) {
      return err.message;
    }
    if (err && typeof err.error === "string" && err.error) {
      return err.error;
    }
    try {
      return JSON.stringify(err);
    } catch (e) {
      return "request failed";
    }
  }

  function apiViaXHR(method, path, body, cb) {
    var xhr = new XMLHttpRequest();
    xhr.open(method, path, true);
    if (method !== "GET") {
      xhr.setRequestHeader("Content-Type", "application/json;charset=UTF-8");
    }
    xhr.onreadystatechange = function () {
      if (xhr.readyState !== 4) return;

      var resp = null;
      if (xhr.responseText) {
        try {
          resp = JSON.parse(xhr.responseText);
        } catch (e) {
          cb(new Error("invalid json"));
          return;
        }
      }

      if (xhr.status >= 200 && xhr.status < 300) {
        cb(null, resp || {});
        return;
      }

      var msg = "request failed";
      if (resp && resp.error) {
        msg = resp.error;
      }
      cb(new Error(msg));
    };
    if (method === "GET") {
      xhr.send(null);
      return;
    }
    xhr.send(body ? JSON.stringify(body) : "{}");
  }

  function api(method, path, body, cb) {
    if (typeof cb !== "function") return;

    var bridge = window.__sbApiCall;
    if (typeof bridge === "function") {
      bridge({
        method: String(method || "GET").toUpperCase(),
        path: String(path || ""),
        body: body == null ? {} : body
      }).then(function (resp) {
        cb(null, resp || {});
      }).catch(function (err) {
        cb(new Error(normalizeBridgeError(err)));
      });
      return;
    }

    apiViaXHR(method, path, body, cb);
  }

  function setStatus(msg) {
    if (!statusNode) return;
    var text = String(msg || "").trim();
    statusNode.textContent = text;
    statusNode.className = text ? "status visible" : "status";
  }

  function renderStartStopIndicator() {
    if (!startStopBtn) return;
    startStopBtn.className = lastRunning ? "control core-running" : "control";
  }

  function normalizeLanguage(raw) {
    var v = String(raw || "").toLowerCase();
    if (v === "en") return "en";
    return "ru";
  }

  function normalizeAutoUpdateHours(raw) {
    var parsed = parseInt(String(raw == null ? "" : raw).trim(), 10);
    if (isNaN(parsed) || parsed < 0) {
      return 0;
    }
    return parsed;
  }

  function normalizeUIScale(raw) {
    var parsed = parseFloat(String(raw == null ? "" : raw).trim());
    if (isNaN(parsed) || parsed < 1) {
      return 1;
    }
    if (parsed > 3) {
      return 3;
    }
    return parsed;
  }

  function applyUIScale(scale) {
    var normalized = normalizeUIScale(scale);
    if (!document || !document.body || !document.body.style) {
      return;
    }
    if (lastAppliedUIScale === normalized) {
      return;
    }
    lastAppliedUIScale = normalized;

    // Native webview already applies DPI scaling. Manual zoom causes
    // double-scaling and breaks layout stretching.
    document.body.style.zoom = "";
    document.body.style.width = "";
    document.body.style.height = "";
  }

  function revealUIAfterInitialState() {
    if (initialStateRendered) return;
    initialStateRendered = true;
    if (!document || !document.body) return;
    var cls = document.body.className || "";
    if (cls.indexOf("ui-loading") < 0) return;
    cls = cls.replace(/\bui-loading\b/g, " ").replace(/\s+/g, " ").trim();
    document.body.className = cls;
  }

  function tr(key) {
    var langDict = I18N[currentLanguage] || I18N.ru;
    if (Object.prototype.hasOwnProperty.call(langDict, key)) {
      return langDict[key];
    }
    return key;
  }

  function applyLanguageUI() {
    document.documentElement.lang = currentLanguage;
    if (settingsTitleNode) settingsTitleNode.textContent = tr("settings");
    if (logsTitleNode) logsTitleNode.textContent = tr("logs");
    if (labelUrlNode) labelUrlNode.textContent = tr("configUrl");
    if (labelVersionNode) labelVersionNode.textContent = tr("version");
    if (labelAutoUpdateNode) labelAutoUpdateNode.textContent = tr("autoUpdate");
    if (labelAutoStartCoreNode) labelAutoStartCoreNode.textContent = tr("autoStartCore");
    if (labelStartMinimizedTrayNode) labelStartMinimizedTrayNode.textContent = tr("startMinimizedTray");
    if (labelProfileNode) labelProfileNode.textContent = tr("profile");
    if (labelRunCheckNode) labelRunCheckNode.textContent = tr("runCheck");
    if (checkConfigBtn) checkConfigBtn.textContent = tr("checkConfig");
    if (newProfileBtn) newProfileBtn.textContent = tr("newProfile");
    if (deleteProfileBtn) deleteProfileBtn.textContent = tr("deleteProfile");
    if (copyLogsBtn) copyLogsBtn.textContent = tr("copyLogs");
    if (logsFilterInput) {
      var filterHint = tr("logsFilterPlaceholder");
      logsFilterInput.placeholder = filterHint;
      logsFilterInput.setAttribute("aria-label", filterHint);
    }
    if (mobileActionsToggleBtn) mobileActionsToggleBtn.textContent = tr("actionsMenu");
    if (mobileActionCheckConfigBtn) mobileActionCheckConfigBtn.textContent = tr("checkConfig");
    if (mobileActionCopyLogsBtn) mobileActionCopyLogsBtn.textContent = tr("copyLogs");
    if (startStopBtn) startStopBtn.textContent = lastRunning ? tr("stop") : tr("start");
    renderStartStopIndicator();
    if (releaseCurrentCaptionNode) releaseCurrentCaptionNode.textContent = tr("releaseCurrent");
    if (releaseLatestCaptionNode) releaseLatestCaptionNode.textContent = tr("releaseLatest");
    if (updateAppBtn) updateAppBtn.textContent = tr("updateApp");
    if (confirmTitleNode) confirmTitleNode.textContent = tr("confirmTitle");
    if (confirmCancelBtn) confirmCancelBtn.textContent = tr("cancel");
    if (confirmOkBtn) confirmOkBtn.textContent = tr("deleteAction");
    renderUptime(lastUptimeSeconds, lastRunning);
    renderAppReleaseMenu(lastAppReleaseTag, lastAppReleaseURL, lastAppUpdateAvailable, lastAppLatestReleaseTag, lastAppLatestReleaseURL);

    if (langRuBtn) {
      langRuBtn.className = currentLanguage === "ru" ? "control lang-btn active" : "control lang-btn";
    }
    if (langEnBtn) {
      langEnBtn.className = currentLanguage === "en" ? "control lang-btn active" : "control lang-btn";
    }

    if (mobileActionsToggleBtn) {
      mobileActionsToggleBtn.setAttribute("aria-label", tr("actionsMenu"));
    }
    setLogsFilterValidation(logsFilterError);
  }

  function setReleaseMenuLink(node, label, href) {
    if (!node) return;
    var text = String(label || "").trim();
    var url = String(href || "").trim();
    var fallback = tr("releaseUnknown");
    if (!text) text = fallback;
    node.textContent = text;
    if (url) {
      node.href = url;
      node.className = "release-menu-value release-menu-link";
      node.title = text;
      node.setAttribute("tabindex", "0");
      return;
    }
    node.removeAttribute("href");
    node.className = "release-menu-value";
    node.title = text;
    node.setAttribute("tabindex", "-1");
  }

  function renderAppReleaseMenu(tag, link, hasUpdate, latestTag, latestLink) {
    if (!releaseMenuToggleBtn || !releaseMenuNode) return;
    var normalizedTag = String(tag || "").trim();
    var normalizedLink = String(link || "").trim();
    var normalizedLatestTag = String(latestTag || "").trim();
    var normalizedLatestLink = String(latestLink || "").trim();
    var releasesRoot = "https://github.com/Adam-Sizzler/singbox-gui/releases";
    var updateAvailable = !!hasUpdate;
    var showLatest = updateAvailable && normalizedLatestTag !== "" && normalizedLatestTag !== normalizedTag;

    releaseMenuToggleBtn.hidden = false;
    if (releaseMenuToggleArrowBtn) {
      releaseMenuToggleArrowBtn.hidden = false;
    }
    if (releaseMenuLabelNode) {
      releaseMenuLabelNode.textContent = normalizedTag || tr("releaseButton");
    }
    if (releaseMenuToggleBtn) {
      var title = normalizedTag || tr("releaseUnknown");
      if (updateAvailable && normalizedLatestTag) {
        title = normalizedTag + " -> " + normalizedLatestTag;
      }
      releaseMenuToggleBtn.title = title;
    }
    if (releaseMenuToggleArrowBtn) {
      releaseMenuToggleArrowBtn.title = releaseMenuToggleBtn ? releaseMenuToggleBtn.title : "";
    }

    lastAppUpdateAvailable = updateAvailable;
    applyReleaseMenuToggleState();

    setReleaseMenuLink(releaseCurrentLinkNode, normalizedTag || tr("releaseUnknown"), normalizedLink || releasesRoot);
    if (releaseLatestRowNode) {
      releaseLatestRowNode.hidden = !showLatest;
    }
    if (showLatest) {
      setReleaseMenuLink(releaseLatestLinkNode, normalizedLatestTag, normalizedLatestLink || releasesRoot);
    }

    if (updateAppBtn) {
      updateAppBtn.hidden = !showLatest;
      updateAppBtn.disabled = !showLatest || lastBusy || appUpdateInFlight;
    }
  }

  function applyReleaseMenuToggleState() {
    if (releaseMenuToggleBtn) {
      var labelClass = "control release-menu-toggle";
      if (releaseMenuOpened) labelClass += " open";
      if (lastAppUpdateAvailable) labelClass += " status-dot-active";
      releaseMenuToggleBtn.className = labelClass;
    }
    if (releaseMenuToggleArrowBtn) {
      var arrowClass = "control release-menu-toggle-arrow";
      if (releaseMenuOpened) arrowClass += " open";
      releaseMenuToggleArrowBtn.className = arrowClass;
      releaseMenuToggleArrowBtn.setAttribute("aria-expanded", releaseMenuOpened ? "true" : "false");
    }
  }

  function openReleaseMenu() {
    if (!releaseMenuToggleBtn || !releaseMenuNode) return;
    releaseMenuNode.hidden = false;
    releaseMenuOpened = true;
    applyReleaseMenuToggleState();
  }

  function closeReleaseMenu() {
    if (!releaseMenuToggleBtn || !releaseMenuNode) return;
    releaseMenuNode.hidden = true;
    releaseMenuOpened = false;
    applyReleaseMenuToggleState();
  }

  function toggleReleaseMenu() {
    if (releaseMenuOpened) {
      closeReleaseMenu();
      return;
    }
    openReleaseMenu();
  }

  function renderDefaultStatus(protoWarn) {
    if (protoWarn) {
      setStatus(tr("warnPrefix") + protoWarn);
      return;
    }
    if (lastBusy) {
      setStatus(tr("statusBusy"));
      return;
    }
    setStatus("");
  }

  function formatUptime(seconds) {
    var total = parseInt(seconds, 10);
    if (isNaN(total) || total < 0) total = 0;
    var h = Math.floor(total / 3600);
    var m = Math.floor((total % 3600) / 60);
    var s = total % 60;

    function pad(v) {
      return v < 10 ? "0" + String(v) : String(v);
    }

    if (h > 99) {
      return String(h) + ":" + pad(m) + ":" + pad(s);
    }
    return pad(h) + ":" + pad(m) + ":" + pad(s);
  }

  function renderUptime(uptimeSeconds, running) {
    if (!uptimeNode) return;
    var shown = running ? formatUptime(uptimeSeconds) : "00:00:00";
    uptimeNode.textContent = tr("uptime") + ": " + shown;
  }

  function setLanguage(next, persist) {
    var normalized = normalizeLanguage(next);
    if (normalized === currentLanguage && !persist) {
      return;
    }
    currentLanguage = normalized;
    applyLanguageUI();
    renderDefaultStatus(lastProtoWarn);

    if (!persist) {
      return;
    }

    api("POST", "/api/state", { language: currentLanguage }, function (err, state) {
      if (err) {
        setStatus(tr("errorPrefix") + err.message);
        return;
      }
      renderState(state);
    });
  }

  function normalizeProfileName(profile, idx) {
    var p = profile || {};
    var name = p.name || p.Name || "";
    if (!name) {
      name = "profile-" + String(idx + 1);
    }
    return String(name);
  }

  function sameStringArray(a, b) {
    if (a === b) return true;
    if (!a || !b) return false;
    if (a.length !== b.length) return false;
    for (var i = 0; i < a.length; i++) {
      if (a[i] !== b[i]) return false;
    }
    return true;
  }

  function setSelectedProfile(name) {
    selectedProfile = name || "";
    if (profileValueNode) {
      profileValueNode.textContent = selectedProfile || "-";
    }
    if (profilePicker) {
      profilePicker.title = selectedProfile || "";
    }
    if (profileNameInput) {
      if (document.activeElement !== profileNameInput || (!profileRenameInFlight && !profileRenameTimer)) {
        profileNameInput.value = selectedProfile || "";
      }
      profileNameInput.title = selectedProfile || "";
    }
  }

  function renderProfileMenu() {
    if (!profileMenu) return;
    profileMenu.innerHTML = "";

    for (var i = 0; i < profileNames.length; i++) {
      var name = profileNames[i];
      var item = document.createElement("button");
      item.type = "button";
      item.className = "profile-option" + (name === selectedProfile ? " active" : "");
      item.setAttribute("role", "option");
      item.setAttribute("aria-selected", name === selectedProfile ? "true" : "false");
      item.textContent = name;
      item.onclick = (function (value) {
        return function () {
          closeProfileMenu();
          if (loadingState) return;
          if (!value || value === selectedProfile) return;
          api("POST", "/api/state", { current_profile: value }, function (err, state) {
            if (err) {
              setStatus(tr("errorPrefix") + err.message);
              return;
            }
            renderState(state);
          });
        };
      })(name);
      profileMenu.appendChild(item);
    }
  }

  function openProfileMenu() {
    if (!profileMenu || !profilePicker) return;
    renderProfileMenu();
    profileMenu.hidden = false;
    profileMenu.scrollTop = 0;
    profilePicker.className = "control profile-picker open";
    profilePicker.setAttribute("aria-expanded", "true");
    profileMenuOpened = true;
  }

  function closeProfileMenu() {
    if (!profileMenu || !profilePicker) return;
    profileMenu.hidden = true;
    profilePicker.className = "control profile-picker";
    profilePicker.setAttribute("aria-expanded", "false");
    profileMenuOpened = false;
  }

  function toggleProfileMenu() {
    if (profileMenuOpened) {
      closeProfileMenu();
      return;
    }
    openProfileMenu();
  }

  function isConfirmModalOpen() {
    return !!confirmModal && !confirmModal.hidden;
  }

  function closeConfirmModal() {
    if (!confirmModal || confirmModal.hidden) return;
    confirmModal.hidden = true;
    confirmAction = null;
  }

  function openConfirmModal(message, onConfirm) {
    if (!confirmModal || !confirmMessageNode) {
      if (window.confirm(message || tr("confirmDelete"))) {
        if (typeof onConfirm === "function") onConfirm();
      }
      return;
    }

    confirmMessageNode.textContent = message || "";
    confirmAction = typeof onConfirm === "function" ? onConfirm : null;
    confirmModal.hidden = false;
    if (confirmCancelBtn && confirmCancelBtn.focus) {
      try {
        confirmCancelBtn.focus();
      } catch (e) {}
    }
  }

  function runConfirmAction() {
    if (!isConfirmModalOpen()) return;
    var action = confirmAction;
    closeConfirmModal();
    if (typeof action === "function") {
      action();
    }
  }

  function showToast(kind, message, ttlMs) {
    if (!toastStack || !message) return;

    var tone = kind || "info";
    var toast = document.createElement("div");
    toast.className = "toast toast-" + tone;
    toast.textContent = message;
    toastStack.appendChild(toast);

    setTimeout(function () {
      if (!toast || !toast.parentNode) return;
      toast.className += " visible";
    }, 10);

    var ttl = parseInt(ttlMs, 10);
    if (isNaN(ttl) || ttl < 1200) {
      ttl = tone === "error" ? 3600 : 2400;
    }

    setTimeout(function () {
      if (!toast || !toast.parentNode) return;
      toast.className = toast.className.replace(/\s?visible\b/g, "");
      setTimeout(function () {
        if (toast && toast.parentNode) {
          toast.parentNode.removeChild(toast);
        }
      }, 180);
    }, ttl);
  }

  function setCheckButtonsDisabled(disabled) {
    var next = !!disabled;
    if (checkConfigBtn) checkConfigBtn.disabled = next;
    if (mobileActionCheckConfigBtn) mobileActionCheckConfigBtn.disabled = next;
  }

  function setCopyButtonsDisabled(disabled) {
    var next = !!disabled;
    if (copyLogsBtn) copyLogsBtn.disabled = next;
    if (mobileActionCopyLogsBtn) mobileActionCopyLogsBtn.disabled = next;
  }

  function isMobileActionsMenuOpen() {
    return !!mobileActionsMenu && !mobileActionsMenu.hidden;
  }

  function closeMobileActionsMenu() {
    if (!mobileActionsMenu || mobileActionsMenu.hidden) return;
    mobileActionsMenu.hidden = true;
    mobileActionsOpened = false;
    if (mobileActionsToggleBtn) {
      mobileActionsToggleBtn.setAttribute("aria-expanded", "false");
    }
  }

  function openMobileActionsMenu() {
    if (!mobileActionsMenu) return;
    mobileActionsMenu.hidden = false;
    mobileActionsOpened = true;
    if (mobileActionsToggleBtn) {
      mobileActionsToggleBtn.setAttribute("aria-expanded", "true");
    }
  }

  function toggleMobileActionsMenu() {
    if (isMobileActionsMenuOpen()) {
      closeMobileActionsMenu();
      return;
    }
    openMobileActionsMenu();
  }

  function computeStatePollDelay() {
    if (lastBusy) return STATE_POLL_BUSY_MS;
    if (lastRunning) return STATE_POLL_RUNNING_MS;
    return STATE_POLL_IDLE_MS;
  }

  function scheduleNextStatePoll(delayMs) {
    if (!pollingActive) return;
    if (stateTimer) {
      clearTimeout(stateTimer);
      stateTimer = null;
    }
    var delay = typeof delayMs === "number" ? delayMs : computeStatePollDelay();
    if (delay < 0) delay = 0;
    statePollDelay = delay;
    stateTimer = setTimeout(function () {
      stateTimer = null;
      refreshState(false);
    }, statePollDelay);
  }

  function scheduleNextLogsPoll(delayMs) {
    if (!pollingActive) return;
    if (logsTimer) {
      clearTimeout(logsTimer);
      logsTimer = null;
    }
    var delay = typeof delayMs === "number" ? delayMs : LOGS_POLL_MIN_MS;
    if (delay < LOGS_POLL_MIN_MS) delay = LOGS_POLL_MIN_MS;
    logsPollDelay = delay;
    logsTimer = setTimeout(function () {
      logsTimer = null;
      pollLogs(false);
    }, logsPollDelay);
  }

  function startUptimeTicker() {
    if (uptimeTimer) return;
    uptimeTimer = setInterval(function () {
      if (document.hidden || !lastRunning) return;
      lastUptimeSeconds++;
      renderUptime(lastUptimeSeconds, lastRunning);
    }, 1000);
  }

  function stopUptimeTicker() {
    if (!uptimeTimer) return;
    clearInterval(uptimeTimer);
    uptimeTimer = null;
  }

  function startPolling() {
    if (pollingActive) return;
    pollingActive = true;
    if (logsPollDelay < LOGS_POLL_MIN_MS) {
      logsPollDelay = LOGS_POLL_MIN_MS;
    }
    scheduleNextStatePoll(computeStatePollDelay());
    scheduleNextLogsPoll(logsPollDelay);
    startUptimeTicker();
  }

  function stopPolling() {
    pollingActive = false;
    if (stateTimer) {
      clearTimeout(stateTimer);
      stateTimer = null;
    }
    if (logsTimer) {
      clearTimeout(logsTimer);
      logsTimer = null;
    }
    stopUptimeTicker();
  }

  function runCheckConfigAction() {
    if (lastBusy) return;
    setCheckButtonsDisabled(true);
    api("POST", "/api/action/check-config", {}, function (err, state) {
      if (err) {
        setStatus(tr("errorPrefix") + err.message);
        showToast("error", tr("errorPrefix") + err.message);
        refreshState(true);
        return;
      }
      renderState(state);
      setStatus(tr("statusConfigOk"));
      showToast("success", tr("statusConfigOk"));
      setTimeout(function () {
        renderDefaultStatus(lastProtoWarn);
      }, 1500);
    });
  }

  function runCopyLogsAction() {
    if (copyLogsInFlight) return;
    copyLogsInFlight = true;
    setCopyButtonsDisabled(true);
    api("POST", "/api/action/copy-logs", {}, function (err) {
      copyLogsInFlight = false;
      setCopyButtonsDisabled(false);
      if (err) {
        setStatus(tr("errorPrefix") + err.message);
        showToast("error", tr("errorPrefix") + err.message);
        return;
      }
      setStatus(tr("statusLogsCopied"));
      showToast("success", tr("statusLogsCopied"));
    });
  }

  function runUpdateAppAction() {
    if (appUpdateInFlight || lastBusy || !lastAppUpdateAvailable || !lastAppLatestReleaseTag) return;
    appUpdateInFlight = true;
    if (updateAppBtn) {
      updateAppBtn.disabled = true;
    }
    api("POST", "/api/action/update-app", {}, function (err) {
      appUpdateInFlight = false;
      if (err) {
        setStatus(tr("errorPrefix") + err.message);
        showToast("error", tr("errorPrefix") + err.message);
        refreshState(true);
        return;
      }
      closeReleaseMenu();
      if (updateAppBtn) {
        updateAppBtn.disabled = true;
      }
      setStatus(tr("statusUpdateStarted"));
      showToast("success", tr("statusUpdateStarted"), 4200);
    });
  }

  function renderState(state) {
    loadingState = true;
    var prevLanguage = currentLanguage;
    var prevRunning = lastRunning;
    var prevBusy = lastBusy;
    var prevSelectedProfile = selectedProfile;
    var prevProfileNames = profileNames.slice(0);
    var prevAppReleaseTag = lastAppReleaseTag;
    var prevAppReleaseURL = lastAppReleaseURL;
    var prevAppUpdateAvailable = lastAppUpdateAvailable;
    var prevAppLatestReleaseTag = lastAppLatestReleaseTag;
    var prevAppLatestReleaseURL = lastAppLatestReleaseURL;

    currentLanguage = normalizeLanguage(state.language || currentLanguage);

    var active = state.current_profile || "";
    var profiles = state.profiles || [];
    var nextNames = [];
    for (var i = 0; i < profiles.length; i++) {
      nextNames.push(normalizeProfileName(profiles[i], i));
    }
    profileNames = nextNames;

    var preferred = active || prevSelectedProfile;
    var hasPreferred = false;
    for (var j = 0; j < profileNames.length; j++) {
      if (profileNames[j] === preferred) {
        hasPreferred = true;
        break;
      }
    }
    var nextSelected = "";
    if (hasPreferred) {
      nextSelected = preferred;
    } else if (profileNames.length > 0) {
      nextSelected = profileNames[0];
    }
    setSelectedProfile(nextSelected);

    var profilesChanged = !sameStringArray(prevProfileNames, profileNames);
    var selectedChanged = prevSelectedProfile !== nextSelected;
    if (profilesChanged || (profileMenuOpened && selectedChanged)) {
      renderProfileMenu();
    }

    if (document.activeElement !== urlInput) {
      urlInput.value = state.url || "";
    }
    if (document.activeElement !== versionInput) {
      versionInput.value = state.version || "latest";
    }
    lastAutoUpdateHours = normalizeAutoUpdateHours(state.auto_update_hours);
    if (document.activeElement !== autoUpdateInput) {
      autoUpdateInput.value = String(lastAutoUpdateHours);
    }
    lastAutoStartCore = !!state.auto_start_core;
    if (autoStartCoreInput && !startupPatchInFlight && !startupPatchQueued) {
      autoStartCoreInput.checked = lastAutoStartCore;
    }
    lastStartMinimizedTray = !!state.start_minimized_to_tray;
    if (startMinimizedTrayInput && !startupPatchInFlight && !startupPatchQueued) {
      startMinimizedTrayInput.checked = lastStartMinimizedTray;
    }
    applyUIScale(state.ui_scale);

    lastRunning = !!state.running;
    lastBusy = !!state.busy;
    lastUptimeSeconds = parseInt(state.uptime_seconds || 0, 10);
    if (isNaN(lastUptimeSeconds) || lastUptimeSeconds < 0) lastUptimeSeconds = 0;
    lastAppReleaseTag = String(state.app_release_tag || "").trim();
    lastAppReleaseURL = String(state.app_release_url || "").trim();
    lastAppUpdateAvailable = !!state.app_update_available;
    lastAppLatestReleaseTag = String(state.app_latest_release_tag || "").trim();
    lastAppLatestReleaseURL = String(state.app_latest_release_url || "").trim();
    if (startStopBtn) {
      startStopBtn.disabled = lastBusy;
    }
    setCheckButtonsDisabled(lastBusy);

    var languageChanged = prevLanguage !== currentLanguage;
    var releaseChanged =
      prevAppReleaseTag !== lastAppReleaseTag ||
      prevAppReleaseURL !== lastAppReleaseURL ||
      prevAppUpdateAvailable !== lastAppUpdateAvailable ||
      prevAppLatestReleaseTag !== lastAppLatestReleaseTag ||
      prevAppLatestReleaseURL !== lastAppLatestReleaseURL;

    if (languageChanged) {
      applyLanguageUI();
    } else {
      if (startStopBtn) {
        startStopBtn.textContent = lastRunning ? tr("stop") : tr("start");
      }
      renderStartStopIndicator();
      renderUptime(lastUptimeSeconds, lastRunning);
      if (releaseChanged || prevBusy !== lastBusy || prevRunning !== lastRunning) {
        renderAppReleaseMenu(lastAppReleaseTag, lastAppReleaseURL, lastAppUpdateAvailable, lastAppLatestReleaseTag, lastAppLatestReleaseURL);
      } else if (updateAppBtn) {
        updateAppBtn.disabled = !lastAppUpdateAvailable || lastBusy || appUpdateInFlight || !lastAppLatestReleaseTag;
      }
    }

    lastProtoWarn = state.proto_reg_warn || "";
    renderDefaultStatus(lastProtoWarn);

    revealUIAfterInitialState();
    loadingState = false;
  }

  function refreshState(force) {
    if (document.hidden && !force) {
      if (pollingActive) {
        scheduleNextStatePoll(computeStatePollDelay());
      }
      return;
    }
    if (stateReqInFlight) {
      if (force) {
        stateReqQueued = true;
      }
      return;
    }

    stateReqInFlight = true;
    api("GET", "/api/state", null, function (err, state) {
      stateReqInFlight = false;
      if (err) {
        setStatus(tr("errorPrefix") + err.message);
        revealUIAfterInitialState();
      } else {
        renderState(state);
      }

      if (stateReqQueued) {
        stateReqQueued = false;
        refreshState(true);
        return;
      }

      if (pollingActive) {
        scheduleNextStatePoll(computeStatePollDelay());
      }
    });
  }

  function submitProfileRename() {
    if (!profileNameInput || loadingState) return;
    var nextName = String(profileNameInput.value || "").trim();
    if (!nextName) {
      profileNameInput.value = selectedProfile || "";
      return;
    }
    if (nextName === selectedProfile) return;
    if (profileRenameInFlight) {
      profileRenameQueued = true;
      return;
    }

    profileRenameInFlight = true;
    api("POST", "/api/profile/rename", { name: nextName }, function (err, state) {
      profileRenameInFlight = false;
      if (err) {
        setStatus(tr("errorPrefix") + err.message);
        refreshState(true);
      } else {
        renderState(state);
      }
      if (profileRenameQueued) {
        profileRenameQueued = false;
        submitProfileRename();
      }
    });
  }

  function scheduleProfileRename() {
    if (!profileNameInput || loadingState) return;
    if (profileRenameTimer) {
      clearTimeout(profileRenameTimer);
    }
    profileRenameTimer = setTimeout(function () {
      profileRenameTimer = null;
      submitProfileRename();
    }, 250);
  }

  function saveStateDebounced() {
    if (loadingState) return;
    if (saveTimer) {
      clearTimeout(saveTimer);
    }
    saveTimer = setTimeout(function () {
      saveTimer = null;
      var autoUpdateHours = lastAutoUpdateHours;
      if (autoUpdateInput) {
        var rawHours = String(autoUpdateInput.value || "").trim();
        if (rawHours !== "") {
          autoUpdateHours = normalizeAutoUpdateHours(rawHours);
        }
      }
      api("POST", "/api/state", {
        current_profile: selectedProfile,
        language: currentLanguage,
        url: urlInput.value,
        version: versionInput.value,
        auto_update_hours: autoUpdateHours,
        auto_start_core: !!(autoStartCoreInput && autoStartCoreInput.checked),
        start_minimized_to_tray: !!(startMinimizedTrayInput && startMinimizedTrayInput.checked)
      }, function (err, state) {
        if (err) {
          setStatus(tr("errorPrefix") + err.message);
          return;
        }
        renderState(state);
      });
    }, 350);
  }

  function saveStartupOptionsImmediate() {
    if (loadingState) return;
    if (startupPatchInFlight) {
      startupPatchQueued = true;
      return;
    }
    startupPatchInFlight = true;
    api("POST", "/api/state", {
      auto_start_core: !!(autoStartCoreInput && autoStartCoreInput.checked),
      start_minimized_to_tray: !!(startMinimizedTrayInput && startMinimizedTrayInput.checked)
    }, function (err, state) {
      startupPatchInFlight = false;
      if (err) {
        setStatus(tr("errorPrefix") + err.message);
        refreshState(true);
      } else {
        renderState(state);
      }
      if (startupPatchQueued) {
        startupPatchQueued = false;
        saveStartupOptionsImmediate();
      }
    });
  }

  function createLogSegmentSpan(text, fgClass, fgColor, extraClass) {
    if (!text) return null;
    var span = document.createElement("span");
    span.className = "log-segment";
    if (extraClass) {
      span.className += " " + extraClass;
    }
    if (fgClass) {
      span.className += " " + fgClass;
    }
    if (fgColor) {
      span.style.color = fgColor;
    }
    span.textContent = text;
    return span;
  }

  function appendLogSegment(parent, text, fgClass, fgColor) {
    if (!text) return;
    if (!logsHighlightRegex && !fgClass && !fgColor) {
      parent.appendChild(document.createTextNode(text));
      return;
    }
    if (!logsHighlightRegex) {
      var plain = createLogSegmentSpan(text, fgClass, fgColor, "");
      if (plain) {
        parent.appendChild(plain);
      }
      return;
    }

    logsHighlightRegex.lastIndex = 0;
    var cursor = 0;
    var match;
    while ((match = logsHighlightRegex.exec(text)) !== null) {
      var idx = match.index;
      var val = match[0] || "";

      if (idx > cursor) {
        var before = createLogSegmentSpan(text.substring(cursor, idx), fgClass, fgColor, "");
        if (before) {
          parent.appendChild(before);
        }
      }

      if (val) {
        var hit = createLogSegmentSpan(val, fgClass, fgColor, "log-match");
        if (hit) {
          parent.appendChild(hit);
        }
        cursor = idx + val.length;
      } else {
        if (idx < text.length) {
          var z = createLogSegmentSpan(text.charAt(idx), fgClass, fgColor, "log-match");
          if (z) {
            parent.appendChild(z);
          }
          cursor = idx + 1;
          logsHighlightRegex.lastIndex = idx + 1;
        } else {
          break;
        }
      }

      if (cursor >= text.length) {
        break;
      }
    }

    if (cursor < text.length) {
      var tail = createLogSegmentSpan(text.substring(cursor), fgClass, fgColor, "");
      if (tail) {
        parent.appendChild(tail);
      }
    }
  }

  function xterm256Color(index) {
    var n = parseInt(index, 10);
    if (isNaN(n)) return "";
    if (n < 0) n = 0;
    if (n > 255) n = 255;

    var base = [
      [0, 0, 0], [205, 0, 0], [0, 205, 0], [205, 205, 0],
      [0, 0, 238], [205, 0, 205], [0, 205, 205], [229, 229, 229],
      [127, 127, 127], [255, 0, 0], [0, 255, 0], [255, 255, 0],
      [92, 92, 255], [255, 0, 255], [0, 255, 255], [255, 255, 255]
    ];
    if (n < 16) {
      return "rgb(" + base[n][0] + "," + base[n][1] + "," + base[n][2] + ")";
    }

    if (n >= 232) {
      var g = 8 + (n - 232) * 10;
      return "rgb(" + g + "," + g + "," + g + ")";
    }

    var c = n - 16;
    var r = Math.floor(c / 36);
    var g2 = Math.floor((c % 36) / 6);
    var b = c % 6;
    var level = [0, 95, 135, 175, 215, 255];
    return "rgb(" + level[r] + "," + level[g2] + "," + level[b] + ")";
  }

  function applySGR(state, codesStr) {
    var parts = (codesStr === "" ? "0" : codesStr).split(";");
    for (var i = 0; i < parts.length; i++) {
      var code = parseInt(parts[i], 10);
      if (isNaN(code)) continue;

      if (code === 0 || code === 39) {
        state.fgClass = "";
        state.fgColor = "";
        continue;
      }

      if ((code >= 30 && code <= 37) || (code >= 90 && code <= 97)) {
        state.fgClass = "ansi-fg-" + String(code);
        state.fgColor = "";
        continue;
      }

      if (code === 38 && i + 1 < parts.length) {
        var mode = parseInt(parts[i + 1], 10);
        if (mode === 5 && i + 2 < parts.length) {
          state.fgClass = "";
          state.fgColor = xterm256Color(parts[i + 2]);
          i += 2;
          continue;
        }
        if (mode === 2 && i + 4 < parts.length) {
          var r = parseInt(parts[i + 2], 10);
          var g = parseInt(parts[i + 3], 10);
          var b = parseInt(parts[i + 4], 10);
          if (!isNaN(r) && !isNaN(g) && !isNaN(b)) {
            state.fgClass = "";
            state.fgColor = "rgb(" + r + "," + g + "," + b + ")";
          }
          i += 4;
          continue;
        }
      }
    }
  }

  function renderLogLine(parent, text) {
    var raw = String(text || "");
    var hasAnsiCodes =
      raw.indexOf(ANSI_ESC_RAW_MARKER) >= 0 ||
      raw.indexOf(ANSI_ESC_FALLBACK_MARKER) >= 0;
    if (!logsHighlightRegex && !hasAnsiCodes) {
      if (raw) {
        parent.textContent = raw;
      }
      return;
    }
    var src = raw;
    if (src.indexOf(ANSI_ESC_FALLBACK_MARKER) >= 0) {
      src = src.replace(/\u2190\[/g, "\x1b[");
    }
    ansiCodeRegex.lastIndex = 0;

    var start = 0;
    var state = { fgClass: "", fgColor: "" };
    var match;
    while ((match = ansiCodeRegex.exec(src)) !== null) {
      if (match.index > start) {
        appendLogSegment(parent, src.substring(start, match.index), state.fgClass, state.fgColor);
      }
      applySGR(state, match[1] || "");
      start = ansiCodeRegex.lastIndex;
    }

    if (start < src.length) {
      appendLogSegment(parent, src.substring(start), state.fgClass, state.fgColor);
    } else if (!parent.firstChild && src) {
      appendLogSegment(parent, src, "", "");
    }
  }

  function trimLogBuffer() {
    var overflow = logBuffer.length - MAX_RENDERED_LOG_LINES;
    if (overflow <= 0) return [];
    return logBuffer.splice(0, overflow);
  }

  function removeRenderedLogLines(count) {
    if (!logsNode || count <= 0) return;
    var remaining = count;
    while (remaining > 0 && logsNode.firstChild) {
      logsNode.removeChild(logsNode.firstChild);
      remaining--;
    }
  }

  function trimRenderedLogs() {
    if (!logsNode) return;
    var overflow = logsNode.childElementCount - MAX_RENDERED_LOG_LINES;
    while (overflow > 0 && logsNode.firstChild) {
      logsNode.removeChild(logsNode.firstChild);
      overflow--;
    }
  }

  function buildLogLineNode(text) {
    var lineText = String(text || "");
    var line = document.createElement("div");
    line.className = "log-line";
    var hasAnsiCodes =
      lineText.indexOf(ANSI_ESC_RAW_MARKER) >= 0 ||
      lineText.indexOf(ANSI_ESC_FALLBACK_MARKER) >= 0;
    if (!logsHighlightRegex && !hasAnsiCodes) {
      line.textContent = lineText;
      return line;
    }
    renderLogLine(line, lineText);
    return line;
  }

  function logsFilterMatches(text) {
    if (!logsFilterRegex) return true;
    logsFilterRegex.lastIndex = 0;
    return logsFilterRegex.test(String(text || ""));
  }

  function compileLogsFilter(rawPattern) {
    var pattern = String(rawPattern || "").trim();
    if (!pattern) {
      return { regex: null, highlightRegex: null, error: "" };
    }
    if (pattern.length > MAX_FILTER_PATTERN_LEN) {
      return { regex: null, highlightRegex: null, error: tr("logsFilterTooLong") + " (>" + MAX_FILTER_PATTERN_LEN + ")" };
    }

    var source = pattern;
    var flags = "";
    if (pattern.charAt(0) === "/") {
      var lastSlash = pattern.lastIndexOf("/");
      if (lastSlash > 0) {
        source = pattern.substring(1, lastSlash);
        flags = pattern.substring(lastSlash + 1);
      }
    }

    flags = flags.replace(/g/g, "");
    try {
      var regex = new RegExp(source, flags);
      var highlightRegex = new RegExp(source, flags + "g");
      return { regex: regex, highlightRegex: highlightRegex, error: "" };
    } catch (e) {
      return { regex: null, highlightRegex: null, error: e && e.message ? String(e.message) : "invalid regexp" };
    }
  }

  function setLogsFilterValidation(errorMessage) {
    logsFilterError = String(errorMessage || "");
    if (!logsFilterInput) return;
    logsFilterInput.className = logsFilterError ? "control logs-filter invalid" : "control logs-filter";
    if (logsFilterError) {
      logsFilterInput.title = tr("logsFilterInvalid") + ": " + logsFilterError;
      return;
    }
    logsFilterInput.title = tr("logsFilterPlaceholder");
  }

  function rebuildRenderedLogs(stickToBottom) {
    if (!logsNode) return;

    var stick = typeof stickToBottom === "boolean"
      ? stickToBottom
      : (logsNode.scrollTop + logsNode.clientHeight >= logsNode.scrollHeight - 4);
    var prevScrollTop = logsNode.scrollTop;
    var frag = document.createDocumentFragment();

    for (var i = 0; i < logBuffer.length; i++) {
      var entry = logBuffer[i] || {};
      if (!logsFilterMatches(entry.text || "")) continue;
      frag.appendChild(buildLogLineNode(entry.text || ""));
    }

    logsNode.innerHTML = "";
    logsNode.appendChild(frag);

    if (stick) {
      logsNode.scrollTop = logsNode.scrollHeight;
      return;
    }

    var maxScrollTop = Math.max(0, logsNode.scrollHeight - logsNode.clientHeight);
    logsNode.scrollTop = Math.max(0, Math.min(maxScrollTop, prevScrollTop));
  }

  function applyLogsFilterFromInput() {
    if (!logsFilterInput) return;
    var compiled = compileLogsFilter(logsFilterInput.value);
    if (compiled.error) {
      setLogsFilterValidation(compiled.error);
      return;
    }
    logsFilterRegex = compiled.regex;
    logsHighlightRegex = compiled.highlightRegex;
    setLogsFilterValidation("");
    rebuildRenderedLogs();
  }

  function appendLogs(entries) {
    if (!logsNode || !entries || !entries.length) return;

    var normalized = [];
    for (var i = 0; i < entries.length; i++) {
      var raw = entries[i] || {};
      var text = String(raw.text || "");
      var entry = { text: text };
      normalized.push(entry);
      logBuffer.push(entry);
    }
    if (!normalized.length) return;

    var removedEntries = trimLogBuffer();
    if (logsFilterRegex) {
      var stickFiltered = logsNode.scrollTop + logsNode.clientHeight >= logsNode.scrollHeight - 4;
      var prevFilteredScrollTop = logsNode.scrollTop;

      var removedMatched = 0;
      for (var k = 0; k < removedEntries.length; k++) {
        if (logsFilterMatches(removedEntries[k].text || "")) {
          removedMatched++;
        }
      }
      if (removedMatched > 0) {
        removeRenderedLogLines(removedMatched);
      }

      var filteredFrag = document.createDocumentFragment();
      for (var m = 0; m < normalized.length; m++) {
        if (!logsFilterMatches(normalized[m].text)) continue;
        filteredFrag.appendChild(buildLogLineNode(normalized[m].text));
      }
      logsNode.appendChild(filteredFrag);

      if (stickFiltered) {
        logsNode.scrollTop = logsNode.scrollHeight;
        return;
      }
      var maxFilteredScrollTop = Math.max(0, logsNode.scrollHeight - logsNode.clientHeight);
      logsNode.scrollTop = Math.max(0, Math.min(maxFilteredScrollTop, prevFilteredScrollTop));
      return;
    }

    var stick = logsNode.scrollTop + logsNode.clientHeight >= logsNode.scrollHeight - 4;
    var prevScrollTop = logsNode.scrollTop;
    var frag = document.createDocumentFragment();

    for (var j = 0; j < normalized.length; j++) {
      frag.appendChild(buildLogLineNode(normalized[j].text));
    }

    logsNode.appendChild(frag);
    trimRenderedLogs();
    if (stick) {
      logsNode.scrollTop = logsNode.scrollHeight;
      return;
    }
    var maxScrollTop = Math.max(0, logsNode.scrollHeight - logsNode.clientHeight);
    logsNode.scrollTop = Math.max(0, Math.min(maxScrollTop, prevScrollTop));
  }

  function pollLogs(force) {
    if (document.hidden && !force) {
      if (pollingActive) {
        scheduleNextLogsPoll(LOGS_POLL_MAX_MS);
      }
      return;
    }
    if (logsReqInFlight) return;
    logsReqInFlight = true;
    api("GET", "/api/logs?from=" + lastLogId, null, function (err, data) {
      logsReqInFlight = false;
      if (err) {
        logsPollDelay = LOGS_POLL_ERROR_MS;
        if (pollingActive) {
          scheduleNextLogsPoll(logsPollDelay);
        }
        return;
      }
      var entries = data.entries || [];
      appendLogs(entries);

      var parsedLastId = parseInt(data.last_id, 10);
      if (!isNaN(parsedLastId) && parsedLastId >= 0) {
        lastLogId = parsedLastId;
      }

      if (entries.length > 0) {
        logsPollDelay = LOGS_POLL_MIN_MS;
      } else {
        if (logsPollDelay < LOGS_POLL_MIN_MS) {
          logsPollDelay = LOGS_POLL_MIN_MS;
        }
        logsPollDelay = Math.min(LOGS_POLL_MAX_MS, logsPollDelay + LOGS_POLL_EMPTY_STEP_MS);
      }
      if (pollingActive) {
        scheduleNextLogsPoll(logsPollDelay);
      }
    });
  }

  profilePicker.onclick = function () {
    toggleProfileMenu();
  };

  profilePicker.onmouseenter = function () {
    openProfileMenu();
  };

  if (profileWrap) {
    profileWrap.onmouseleave = function () {
      closeProfileMenu();
    };
  }

  profilePicker.onkeydown = function (e) {
    var key = e.key || "";
    if (key === "Enter" || key === " " || key === "ArrowDown") {
      if (e.preventDefault) e.preventDefault();
      openProfileMenu();
      return;
    }
    if (key === "Escape") {
      if (e.preventDefault) e.preventDefault();
      closeProfileMenu();
    }
  };

  if (releaseMenuToggleBtn && releaseMenuToggleBtn.tagName === "BUTTON") {
    releaseMenuToggleBtn.onclick = function () {
      toggleReleaseMenu();
    };
    releaseMenuToggleBtn.onkeydown = function (e) {
      var key = e.key || "";
      if (key === "Enter" || key === " " || key === "ArrowDown") {
        if (e.preventDefault) e.preventDefault();
        openReleaseMenu();
        return;
      }
      if (key === "Escape") {
        if (e.preventDefault) e.preventDefault();
        closeReleaseMenu();
      }
    };
  }

  if (releaseMenuToggleArrowBtn) {
    releaseMenuToggleArrowBtn.onmouseenter = function () {
      openReleaseMenu();
    };
    releaseMenuToggleArrowBtn.onclick = function () {
      toggleReleaseMenu();
    };
    releaseMenuToggleArrowBtn.onkeydown = function (e) {
      var key = e.key || "";
      if (key === "Enter" || key === " " || key === "ArrowDown") {
        if (e.preventDefault) e.preventDefault();
        openReleaseMenu();
        return;
      }
      if (key === "Escape") {
        if (e.preventDefault) e.preventDefault();
        closeReleaseMenu();
      }
    };
  }

  if (releaseMenuWrap) {
    releaseMenuWrap.onmouseleave = function () {
      closeReleaseMenu();
    };
  }

  if (updateAppBtn) {
    updateAppBtn.onclick = function () {
      runUpdateAppAction();
    };
  }

  document.addEventListener("mousedown", function (e) {
    if (!profileMenuOpened || !profileWrap) return;
    var target = e.target || e.srcElement;
    if (profileWrap.contains && !profileWrap.contains(target)) {
      closeProfileMenu();
    }
  });

  document.addEventListener("mousedown", function (e) {
    if (!releaseMenuOpened || !releaseMenuWrap) return;
    var target = e.target || e.srcElement;
    if (releaseMenuWrap.contains && !releaseMenuWrap.contains(target)) {
      closeReleaseMenu();
    }
  });

  document.addEventListener("mousedown", function (e) {
    if (!mobileActionsOpened || !mobileActionsWrap) return;
    var target = e.target || e.srcElement;
    if (mobileActionsWrap.contains && !mobileActionsWrap.contains(target)) {
      closeMobileActionsMenu();
    }
  });

  document.addEventListener("keydown", function (e) {
    var key = e.key || "";
    if (key === "Escape") {
      if (profileMenuOpened) {
        closeProfileMenu();
      }
      if (releaseMenuOpened) {
        closeReleaseMenu();
      }
      if (mobileActionsOpened) {
        closeMobileActionsMenu();
      }
      if (isConfirmModalOpen()) {
        if (e.preventDefault) e.preventDefault();
        closeConfirmModal();
      }
      return;
    }

    if ((key === "Enter" || key === "NumpadEnter") && isConfirmModalOpen()) {
      if (document.activeElement !== confirmCancelBtn) {
        if (e.preventDefault) e.preventDefault();
        runConfirmAction();
      }
    }
  });

  if (confirmModalOverlay) {
    confirmModalOverlay.onclick = function () {
      closeConfirmModal();
    };
  }

  if (confirmCancelBtn) {
    confirmCancelBtn.onclick = function () {
      closeConfirmModal();
    };
  }

  if (confirmOkBtn) {
    confirmOkBtn.onclick = function () {
      runConfirmAction();
    };
  }

  if (mobileActionsToggleBtn) {
    mobileActionsToggleBtn.onclick = function () {
      toggleMobileActionsMenu();
    };
  }

  if (mobileActionCheckConfigBtn) {
    mobileActionCheckConfigBtn.onclick = function () {
      closeMobileActionsMenu();
      runCheckConfigAction();
    };
  }

  if (mobileActionCopyLogsBtn) {
    mobileActionCopyLogsBtn.onclick = function () {
      closeMobileActionsMenu();
      runCopyLogsAction();
    };
  }

  langRuBtn.onclick = function () {
    if (currentLanguage === "ru") return;
    setLanguage("ru", true);
  };

  langEnBtn.onclick = function () {
    if (currentLanguage === "en") return;
    setLanguage("en", true);
  };

  if (profileNameInput) {
    profileNameInput.oninput = scheduleProfileRename;
    profileNameInput.onchange = submitProfileRename;
    profileNameInput.onblur = submitProfileRename;
  }

  urlInput.oninput = saveStateDebounced;
  versionInput.oninput = saveStateDebounced;
  autoUpdateInput.oninput = saveStateDebounced;
  if (logsFilterInput) {
    logsFilterInput.oninput = function () {
      if (logsFilterTimer) {
        clearTimeout(logsFilterTimer);
      }
      logsFilterTimer = setTimeout(function () {
        logsFilterTimer = null;
        applyLogsFilterFromInput();
      }, 120);
    };
    logsFilterInput.onchange = function () {
      if (logsFilterTimer) {
        clearTimeout(logsFilterTimer);
        logsFilterTimer = null;
      }
      applyLogsFilterFromInput();
    };
  }
  if (autoStartCoreInput) {
    autoStartCoreInput.onchange = saveStartupOptionsImmediate;
  }
  if (startMinimizedTrayInput) {
    startMinimizedTrayInput.onchange = saveStartupOptionsImmediate;
  }
  autoUpdateInput.onblur = function () {
    if (!autoUpdateInput) return;
    autoUpdateInput.value = String(normalizeAutoUpdateHours(autoUpdateInput.value));
  };

  newProfileBtn.onclick = function () {
    api("POST", "/api/profile/new", { name: "" }, function (err, state) {
      if (err) {
        setStatus(tr("errorPrefix") + err.message);
        return;
      }
      renderState(state);
      if (urlInput) {
        urlInput.value = "";
      }
      if (versionInput) {
        versionInput.value = "";
        try {
          versionInput.focus();
          versionInput.select();
        } catch (e) {}
      }
    });
  };

  deleteProfileBtn.onclick = function () {
    if (lastBusy) return;
    openConfirmModal(tr("confirmDelete"), function () {
      api("POST", "/api/profile/delete", { name: selectedProfile }, function (err, state) {
        if (err) {
          setStatus(tr("errorPrefix") + err.message);
          return;
        }
        renderState(state);
      });
    });
  };

  startStopBtn.onclick = function () {
    startStopBtn.disabled = true;
    api("POST", "/api/action/start-stop", {}, function (err, state) {
      if (err) {
        setStatus(tr("errorPrefix") + err.message);
        showToast("error", tr("errorPrefix") + err.message);
        startStopBtn.disabled = false;
        return;
      }
      renderState(state);
      refreshState(true);
    });
  };

  if (checkConfigBtn) {
    checkConfigBtn.onclick = function () {
      runCheckConfigAction();
    };
  }

  copyLogsBtn.onclick = function () {
    runCopyLogsAction();
  };

  function syncStateAndLogs(force) {
    if (force) {
      logsPollDelay = LOGS_POLL_MIN_MS;
    }
    refreshState(!!force);
    pollLogs(!!force);
  }

  document.addEventListener("visibilitychange", function () {
    if (document.hidden) {
      stopPolling();
      closeReleaseMenu();
      closeMobileActionsMenu();
      return;
    }
    var now = Date.now();
    if (now - lastVisibilitySyncAt < 600) {
      startPolling();
      return;
    }
    lastVisibilitySyncAt = now;
    syncStateAndLogs(false);
    startPolling();
  });

  // Always perform first sync to remove loading veil even if window was
  // initially hidden by the host before first show.
  lastVisibilitySyncAt = Date.now();
  syncStateAndLogs(true);
  if (!document.hidden) {
    startPolling();
  }

  window.onbeforeunload = function () {
    stopPolling();
    if (saveTimer) clearTimeout(saveTimer);
    if (logsFilterTimer) clearTimeout(logsFilterTimer);
    if (profileRenameTimer) clearTimeout(profileRenameTimer);
    closeReleaseMenu();
    closeMobileActionsMenu();
    closeConfirmModal();
  };
})();
