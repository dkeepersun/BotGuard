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
(function () {
  var script = document.currentScript;
  var base = script && script.src ? script.src.replace(/[^/]*$/, '') : '';
  var ready = import(base + 'botguard.js');
  window.initBotGuard = function (config, onload, onerror) {
    ready.then(function (m) {
      m.initBotGuard(config, onload, onerror);
    }, function (err) {
      if (onerror) onerror(err);
    });
  };
})();
