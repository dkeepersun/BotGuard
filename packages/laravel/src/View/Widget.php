<?php

declare(strict_types=1);

namespace BotGuard\Laravel\View;

use Illuminate\View\Component;

/**
 * <x-botguard action="login" />                         embedded widget
 * <x-botguard action="login" mode="popup" bind="#submit" />  verify when the button is clicked
 *
 * Loads the widget script from the gateway (like Yidun's hosted script); only
 * the public app_key reaches the page.
 */
final class Widget extends Component
{
    public string $endpoint;
    public string $appKey;
    public string $field;

    public function __construct(
        public string $action,
        public string $mode = 'embed',
        public ?string $bind = null,
    ) {
        $this->endpoint = rtrim((string) config('botguard.public_endpoint'), '/');
        $this->appKey = (string) config('botguard.app_key');
        $this->field = (string) config('botguard.field', 'botguard_token');
    }

    public function render(): string
    {
        return <<<'BLADE'
@once
<script type="module" src="{{ $endpoint }}/sdk/v1/botguard.js"></script>
@endonce
@if ($bind)
<script type="module">
  import { init } from @json($endpoint . '/sdk/v1/botguard.js');
  init({ endpoint: @json($endpoint), appKey: @json($appKey), action: @json($action),
         fieldName: @json($field), mode: 'popup', bindTo: @json($bind) });
</script>
@else
<botguard-widget endpoint="{{ $endpoint }}" app-key="{{ $appKey }}" action="{{ $action }}" field-name="{{ $field }}" mode="{{ $mode }}"></botguard-widget>
@endif
BLADE;
    }
}
