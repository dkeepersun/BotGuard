// Classic-script loader, the counterpart of Yidun's load.min.js:
//
//   <script src="https://botguard.example.com/sdk/v1/load.js"></script>
//   <script>
//     initBotGuard({ element: '#captcha', endpoint: 'https://botguard.example.com',
//       appKey: 'app_xxx', action: 'login', onVerify(err, ret) { ... } },
//       function onload(instance) {}, function onerror(err) {});
//   </script>
//
// It loads the module next to it and forwards the call once it is ready.
// With <script async> the page may call initBotGuard before this file has
// run; a one-line stub in front of it queues those calls:
//
//   <script>window.initBotGuard=window.initBotGuard||function(){(initBotGuard.q=initBotGuard.q||[]).push(arguments)}</script>
//   <script async src="https://botguard.example.com/sdk/v1/load.js"></script>
(function () {
  var queued = (window.initBotGuard && window.initBotGuard.q) || [];
  var script = document.currentScript;
  if (!script || !script.src) {
    // currentScript is null when this runs from a callback or was injected
    // in an unusual way: look for our own tag instead.
    var all = document.getElementsByTagName('script');
    for (var i = all.length - 1; i >= 0; i--) {
      if (/\/load(\.min)?\.js([?#]|$)/.test(all[i].src)) {
        script = all[i];
        break;
      }
    }
  }
  var ready;
  if (script && script.src) {
    ready = import(script.src.replace(/[?#].*$/, '').replace(/[^/]*$/, '') + 'botguard.js');
  } else {
    ready = Promise.reject(new Error('BotGuard load.js：无法确定脚本地址（document.currentScript 为空）。' +
      '请用普通的 <script src=".../sdk/v1/load.js"> 引入，不要用 type="module" 或把它内联、打包进其他脚本；' +
      '用 npm 包时请直接 import botguard.js'));
  }
  function initBotGuard(config, onload, onerror) {
    ready.then(function (m) {
      m.initBotGuard(config, onload, onerror);
    }, function (err) {
      if (onerror) onerror(err);
      else if (window.console) console.error(err);
    });
  }
  window.initBotGuard = initBotGuard;
  for (var j = 0; j < queued.length; j++) initBotGuard.apply(null, queued[j]);
})();
