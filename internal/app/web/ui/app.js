(function () {
  var urlInput = document.getElementById("url");
  var versionInput = document.getElementById("version");
  var profileWrap = document.getElementById("profileWrap");
  var profilePicker = document.getElementById("profilePicker");
  var profileValueNode = document.getElementById("profileValue");
  var profileMenu = document.getElementById("profileMenu");
  var newProfileBtn = document.getElementById("newProfile");
  var deleteProfileBtn = document.getElementById("deleteProfile");
  var startStopBtn = document.getElementById("startStop");
  var copyLogsBtn = document.getElementById("copyLogs");
  var statusNode = document.getElementById("status");
  var logsNode = document.getElementById("logs");
  var settingsTitleNode = document.getElementById("settingsTitle");
  var logsTitleNode = document.getElementById("logsTitle");
  var labelUrlNode = document.getElementById("labelUrl");
  var labelVersionNode = document.getElementById("labelVersion");
  var labelProfileNode = document.getElementById("labelProfile");
  var labelLanguageNode = document.getElementById("labelLanguage");
  var langRuBtn = document.getElementById("langRu");
  var langEnBtn = document.getElementById("langEn");

  var lastLogId = 0;
  var stateTimer = null;
  var logsTimer = null;
  var saveTimer = null;
  var loadingState = false;
  var ansiCodeRegex = /\x1b\[([0-9;]*)m/g;
  var profileNames = [];
  var selectedProfile = "";
  var profileMenuOpened = false;
  var currentLanguage = "ru";
  var lastRunning = false;
  var lastBusy = false;
  var lastProtoWarn = "";

  var I18N = {
    ru: {
      settings: "Настройки",
      logs: "Логи",
      configUrl: "Config URL:",
      version: "Версия sing-box:",
      profile: "Профиль:",
      language: "Язык:",
      newProfile: "Новый",
      deleteProfile: "Удалить",
      start: "Старт",
      stop: "Стоп",
      copyLogs: "Копировать логи",
      statusBusy: "Выполняется операция...",
      statusRunning: "sing-box запущен",
      statusStopped: "sing-box остановлен",
      statusLogsCopied: "Логи скопированы в буфер обмена",
      confirmDelete: "Удалить текущий профиль?",
      warnPrefix: "WARN: ",
      errorPrefix: "ERROR: "
    },
    en: {
      settings: "Settings",
      logs: "Logs",
      configUrl: "Config URL:",
      version: "sing-box version:",
      profile: "Profile:",
      language: "Language:",
      newProfile: "New",
      deleteProfile: "Delete",
      start: "Start",
      stop: "Stop",
      copyLogs: "Copy Logs",
      statusBusy: "Operation in progress...",
      statusRunning: "sing-box is running",
      statusStopped: "sing-box is stopped",
      statusLogsCopied: "Logs copied to clipboard",
      confirmDelete: "Delete current profile?",
      warnPrefix: "WARN: ",
      errorPrefix: "ERROR: "
    }
  };

  function api(method, path, body, cb) {
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

  function setStatus(msg) {
    statusNode.textContent = msg || "";
  }

  function normalizeLanguage(raw) {
    var v = String(raw || "").toLowerCase();
    if (v === "en") return "en";
    return "ru";
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
    if (labelProfileNode) labelProfileNode.textContent = tr("profile");
    if (labelLanguageNode) labelLanguageNode.textContent = tr("language");
    if (newProfileBtn) newProfileBtn.textContent = tr("newProfile");
    if (deleteProfileBtn) deleteProfileBtn.textContent = tr("deleteProfile");
    if (copyLogsBtn) copyLogsBtn.textContent = tr("copyLogs");
    if (startStopBtn) startStopBtn.textContent = lastRunning ? tr("stop") : tr("start");

    if (langRuBtn) {
      langRuBtn.className = currentLanguage === "ru" ? "control lang-btn active" : "control lang-btn";
    }
    if (langEnBtn) {
      langEnBtn.className = currentLanguage === "en" ? "control lang-btn active" : "control lang-btn";
    }
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
    setStatus(lastRunning ? tr("statusRunning") : tr("statusStopped"));
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

  function setSelectedProfile(name) {
    selectedProfile = name || "";
    if (profileValueNode) {
      profileValueNode.textContent = selectedProfile || "-";
    }
    if (profilePicker) {
      profilePicker.title = selectedProfile || "";
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

  function renderState(state) {
    loadingState = true;

    currentLanguage = normalizeLanguage(state.language || currentLanguage);

    var active = state.current_profile || "";
    var profiles = state.profiles || [];
    var nextNames = [];
    for (var i = 0; i < profiles.length; i++) {
      nextNames.push(normalizeProfileName(profiles[i], i));
    }
    profileNames = nextNames;

    var preferred = active || selectedProfile;
    var hasPreferred = false;
    for (var j = 0; j < profileNames.length; j++) {
      if (profileNames[j] === preferred) {
        hasPreferred = true;
        break;
      }
    }
    if (hasPreferred) {
      setSelectedProfile(preferred);
    } else if (profileNames.length > 0) {
      setSelectedProfile(profileNames[0]);
    } else {
      setSelectedProfile("");
    }
    renderProfileMenu();

    if (document.activeElement !== urlInput) {
      urlInput.value = state.url || "";
    }
    if (document.activeElement !== versionInput) {
      versionInput.value = state.version || "latest";
    }

    lastRunning = !!state.running;
    lastBusy = !!state.busy;
    startStopBtn.textContent = lastRunning ? tr("stop") : tr("start");
    startStopBtn.disabled = lastBusy;
    applyLanguageUI();
    lastProtoWarn = state.proto_reg_warn || "";
    renderDefaultStatus(lastProtoWarn);

    loadingState = false;
    resizeLogs();
  }

  function refreshState() {
    api("GET", "/api/state", null, function (err, state) {
      if (err) {
        setStatus(tr("errorPrefix") + err.message);
        return;
      }
      renderState(state);
    });
  }

  function saveStateDebounced() {
    if (loadingState) return;
    if (saveTimer) {
      clearTimeout(saveTimer);
    }
    saveTimer = setTimeout(function () {
      saveTimer = null;
      api("POST", "/api/state", {
        current_profile: selectedProfile,
        language: currentLanguage,
        url: urlInput.value,
        version: versionInput.value
      }, function (err, state) {
        if (err) {
          setStatus(tr("errorPrefix") + err.message);
          return;
        }
        renderState(state);
      });
    }, 350);
  }

  function appendLogSegment(parent, text, fgClass, fgColor) {
    if (!text) return;
    var span = document.createElement("span");
    span.className = "log-segment";
    if (fgClass) {
      span.className += " " + fgClass;
    }
    if (fgColor) {
      span.style.color = fgColor;
    }
    span.textContent = text;
    parent.appendChild(span);
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
    var src = (text || "").replace(/\u2190\[/g, "\x1b[");
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

  function appendLogs(entries) {
    if (!entries || !entries.length) return;

    var stick = logsNode.scrollTop + logsNode.clientHeight >= logsNode.scrollHeight - 4;
    var frag = document.createDocumentFragment();

    for (var i = 0; i < entries.length; i++) {
      var text = entries[i].text || "";
      var line = document.createElement("div");
      line.className = "log-line";
      renderLogLine(line, text);
      frag.appendChild(line);
    }

    logsNode.appendChild(frag);
    if (stick) {
      logsNode.scrollTop = logsNode.scrollHeight;
    }
  }

  function resizeLogs() {
    if (!logsNode || !logsNode.getBoundingClientRect) return;
    var rect = logsNode.getBoundingClientRect();
    var vh = window.innerHeight || document.documentElement.clientHeight || document.body.clientHeight || 0;
    var available = vh - rect.top - 10;
    if (available < 120) {
      available = 120;
    }
    logsNode.style.height = String(Math.floor(available)) + "px";
  }

  function pollLogs() {
    api("GET", "/api/logs?from=" + lastLogId, null, function (err, data) {
      if (err) {
        return;
      }
      appendLogs(data.entries || []);
      lastLogId = data.last_id || lastLogId;
    });
  }

  profilePicker.onclick = function () {
    toggleProfileMenu();
  };

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

  document.addEventListener("mousedown", function (e) {
    if (!profileMenuOpened || !profileWrap) return;
    var target = e.target || e.srcElement;
    if (profileWrap.contains && !profileWrap.contains(target)) {
      closeProfileMenu();
    }
  });

  document.addEventListener("keydown", function (e) {
    var key = e.key || "";
    if (key === "Escape" && profileMenuOpened) {
      closeProfileMenu();
    }
  });

  langRuBtn.onclick = function () {
    if (currentLanguage === "ru") return;
    setLanguage("ru", true);
  };

  langEnBtn.onclick = function () {
    if (currentLanguage === "en") return;
    setLanguage("en", true);
  };

  urlInput.oninput = saveStateDebounced;
  versionInput.oninput = saveStateDebounced;

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
    if (!window.confirm(tr("confirmDelete"))) return;
    api("POST", "/api/profile/delete", { name: selectedProfile }, function (err, state) {
      if (err) {
        setStatus(tr("errorPrefix") + err.message);
        return;
      }
      renderState(state);
    });
  };

  startStopBtn.onclick = function () {
    startStopBtn.disabled = true;
    api("POST", "/api/action/start-stop", {}, function (err, state) {
      if (err) {
        setStatus(tr("errorPrefix") + err.message);
        startStopBtn.disabled = false;
        return;
      }
      renderState(state);
      refreshState();
    });
  };

  copyLogsBtn.onclick = function () {
    api("POST", "/api/action/copy-logs", {}, function (err) {
      if (err) {
        setStatus(tr("errorPrefix") + err.message);
        return;
      }
      setStatus(tr("statusLogsCopied"));
    });
  };

  refreshState();
  pollLogs();
  resizeLogs();

  stateTimer = setInterval(refreshState, 1500);
  logsTimer = setInterval(pollLogs, 400);
  window.onresize = resizeLogs;

  window.onbeforeunload = function () {
    if (stateTimer) clearInterval(stateTimer);
    if (logsTimer) clearInterval(logsTimer);
    if (saveTimer) clearTimeout(saveTimer);
  };
})();
