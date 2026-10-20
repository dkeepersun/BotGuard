<?php

// php artisan vendor:publish --tag=botguard-config
return [
    // Gateway URL this server calls for /v1/verify (server to server).
    'endpoint' => env('BOTGUARD_ENDPOINT', 'http://127.0.0.1:8090'),

    // Gateway URL the browser loads the widget from and talks to. Usually the
    // same public HTTPS address; differs when the server reaches the gateway
    // over an internal network.
    'public_endpoint' => env('BOTGUARD_PUBLIC_ENDPOINT', env('BOTGUARD_ENDPOINT', 'http://127.0.0.1:8090')),

    // From `botguard-ctl app create` (public, safe in pages) and
    // `botguard-ctl secret create` (server only: never in views or JS).
    'app_key' => env('BOTGUARD_APP_KEY'),
    'secret_id' => env('BOTGUARD_SECRET_ID'),
    'secret_key' => env('BOTGUARD_SECRET_KEY'),

    // Seconds. Empty, zero, negative or non-numeric values fall back to the
    // defaults, values under 0.05 are raised to 0.05: a verify call is never
    // left without a time limit (cURL treats 0 as "wait forever").
    'timeout' => env('BOTGUARD_TIMEOUT', 3.0),
    'connect_timeout' => env('BOTGUARD_CONNECT_TIMEOUT', 1.0),

    // Extra attempts after a network error or 5xx, re-signed with the same request_id.
    'retries' => (int) env('BOTGUARD_RETRIES', 1),

    // Form field the widget fills with the token.
    'field' => 'botguard_token',
];
