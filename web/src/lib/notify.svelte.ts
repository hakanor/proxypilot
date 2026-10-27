// Desktop notifications. Until they are allowed the toolbar bell keeps nagging.
const supported = typeof Notification !== 'undefined';

export const notify = $state({ permission: supported ? Notification.permission : ('denied' as NotificationPermission), supported });

export async function enableNotifications() {
  if (!supported) return;
  notify.permission = await Notification.requestPermission();
}

/** Shows a notification when the tab is not focused; same tag replaces, so repeats are harmless. */
export function sendNotification(title: string, body: string, tag: string, force = false) {
  if (!supported) return;
  notify.permission = Notification.permission; // may have changed in the browser's site settings
  if (notify.permission !== 'granted' || (!force && document.hasFocus())) return;
  const n = new Notification(title, { body, tag, icon: '/icon.svg' });
  n.onclick = () => {
    window.focus();
    n.close();
  };
}
