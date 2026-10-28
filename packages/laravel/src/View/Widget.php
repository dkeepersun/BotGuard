<?php

declare(strict_types=1);

namespace BotGuard\Laravel\View;

use Illuminate\Container\Container;
use Illuminate\View\Component;

/**
 * <x-botguard action="login" />                              embedded widget
 * <x-botguard action="login" mode="float" />                 trigger: the puzzle floats over the page on hover
 * <x-botguard action="login" mode="inline" />                embedded, the puzzle loads with the page
 * <x-botguard action="login" bind="#submit" />               popup, verify when the button is clicked
 * <x-botguard action="login" mode="embed" bind="#submit" />  embedded, and the button verifies first if needed
 * <x-botguard action="login" mode="popup" />                 popup widget, opened from your own JS via verify()
 * <x-botguard action="login" size="compact" width="100%" image-height="120" />  H5 / app WebView
 * <x-botguard action="login" class="mt-4" id="captcha" />    extra attributes go on the <botguard-widget> element
 *
 * mode, when given, always wins; without it the widget embeds, or pops up
 * when bind is set. Loads the widget script from the gateway (like Yidun's
 * hosted script); only the public app_key reaches the page.
 */
final class Widget extends Component
{
    /** Attributes the component sets itself; extra attributes cannot override them. */
    private const RESERVED = ['endpoint', 'app-key', 'action', 'field-name', 'mode', 'width', 'image-height', 'size', 'accent'];

    public string $endpoint;
    public string $appKey;
    public string $field;
    /** Effective mode: "embed", "float", "inline" or "popup". */
    public string $mode;
    public ?string $bind;

    public function __construct(
        public string $action,
        ?string $mode = null,
        ?string $bind = null,
        public ?string $width = null,
        public ?string $imageHeight = null,
        public ?string $size = null,
        public ?string $accent = null,
    ) {
        $config = Container::getInstance()->make('config');
        $this->endpoint = rtrim((string) $config->get('botguard.public_endpoint'), '/');
        $this->appKey = (string) $config->get('botguard.app_key');
        $this->field = (string) $config->get('botguard.field', 'botguard_token');
        $this->bind = $bind === '' ? null : $bind;
        $mode = $mode === null || $mode === '' ? ($this->bind !== null ? 'popup' : 'embed') : strtolower($mode);
        if (!in_array($mode, ['embed', 'float', 'inline', 'popup'], true)) {
            throw new \InvalidArgumentException('<x-botguard> 的 mode 只能是 embed、float、inline 或 popup');
        }
        $this->mode = $mode;
    }

    /**
     * Extra attributes (class, id, data-*, auto ...) for the element that
     * init() creates in bind mode, as name => value strings.
     *
     * @return array<string, string>
     */
    public function elementAttributes(): array
    {
        $out = [];
        foreach ($this->attributes?->except(self::RESERVED)->getAttributes() ?? [] as $name => $value) {
            if (!is_string($name) || preg_match('/\A[A-Za-z_][-A-Za-z0-9_.:]*\z/', $name) !== 1
                || $value === null || $value === false || !(is_scalar($value) || $value instanceof \Stringable)) {
                continue;
            }
            $out[$name] = $value === true ? '' : (string) $value;
        }
        return $out;
    }

    public function render(): string
    {
        return <<<'BLADE'
@once
<script type="module" src="{{ $endpoint }}/sdk/v1/botguard.js"></script>
@endonce
@if ($bind !== null)
<script type="module">
  import { init } from @json($endpoint . '/sdk/v1/botguard.js');
  const bg = init({ endpoint: @json($endpoint), appKey: @json($appKey), action: @json($action),
         fieldName: @json($field), mode: @json($mode), bindTo: @json($bind),
         width: @json($width), imageHeight: @json($imageHeight), size: @json($size), accent: @json($accent) });
  for (const [name, value] of Object.entries(@json($elementAttributes()))) bg.element.setAttribute(name, value);
</script>
@else
<botguard-widget endpoint="{{ $endpoint }}" app-key="{{ $appKey }}" action="{{ $action }}" field-name="{{ $field }}" mode="{{ $mode }}"
  @if ($width) width="{{ $width }}" @endif @if ($imageHeight) image-height="{{ $imageHeight }}" @endif
  @if ($size) size="{{ $size }}" @endif @if ($accent) accent="{{ $accent }}" @endif {{ $attributes->except(['endpoint', 'app-key', 'field-name']) }}></botguard-widget>
@endif
BLADE;
    }
}
