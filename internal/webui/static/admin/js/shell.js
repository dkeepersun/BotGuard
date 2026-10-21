// Starts the PearAdmin shell and wires the user menu. No inline script, so
// the page works under a strict CSP.
layui.use(['admin', 'jquery'], function () {
  var admin = layui.admin, $ = layui.jquery;
  admin.setConfigType('json');
  admin.setConfigPath('/pear-config');
  admin.render();
  $('body').on('click', '.bg-open', function () {
    admin.jump(this.getAttribute('data-id'), this.getAttribute('data-title'), this.getAttribute('data-href'));
  });
  $('body').on('click', '.bg-logout', function () {
    document.getElementById('bg-logout-form').submit();
  });
});
