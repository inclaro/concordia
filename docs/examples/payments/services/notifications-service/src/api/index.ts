export function sendAlert(channel: string, message: string): boolean {
  return channel.length > 0 && message.length > 0;
}
