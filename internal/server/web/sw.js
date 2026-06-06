'use strict';

// Service worker for the MEATER Monitor. Its job is to let the page raise
// system-level notifications that survive the tab being backgrounded or the
// phone screen being off, and to focus/open the UI when one is tapped.

self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (event) => event.waitUntil(self.clients.claim()));

// Focus an existing tab (or open one) when a notification is clicked.
self.addEventListener('notificationclick', (event) => {
	event.notification.close();
	event.waitUntil((async () => {
		const all = await self.clients.matchAll({ type: 'window', includeUncontrolled: true });
		for (const client of all) {
			if ('focus' in client) return client.focus();
		}
		if (self.clients.openWindow) return self.clients.openWindow('/');
	})());
});

// Future-proofing for true Web Push (browser fully closed): if a push arrives,
// display it. Page-driven notifications don't use this path.
self.addEventListener('push', (event) => {
	let data = { title: 'MEATER alert', body: '' };
	try {
		if (event.data) data = { ...data, ...event.data.json() };
	} catch { /* plain text or empty */ }
	event.waitUntil(self.registration.showNotification(data.title, {
		body: data.body,
		tag: data.tag || 'meater',
		renotify: true,
	}));
});
