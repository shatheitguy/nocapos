// Places people pick in the installer's first step: each sets the time zone
// and keyboard (both can still be changed right there).

export interface Region {
  id: string; // ISO country code
  name: string;
  zone: string;
  keyboard: string;
}

export const REGIONS: Region[] = [
  { id: 'US', name: 'United States', zone: 'America/New_York', keyboard: 'us' },
  { id: 'GB', name: 'United Kingdom', zone: 'Europe/London', keyboard: 'gb' },
  { id: 'AE', name: 'United Arab Emirates', zone: 'Asia/Dubai', keyboard: 'us' },
  { id: 'SA', name: 'Saudi Arabia', zone: 'Asia/Riyadh', keyboard: 'us' },
  { id: 'IN', name: 'India', zone: 'Asia/Kolkata', keyboard: 'us' },
  { id: 'PK', name: 'Pakistan', zone: 'Asia/Karachi', keyboard: 'us' },
  { id: 'LK', name: 'Sri Lanka', zone: 'Asia/Colombo', keyboard: 'us' },
  { id: 'CA', name: 'Canada', zone: 'America/Toronto', keyboard: 'us' },
  { id: 'AU', name: 'Australia', zone: 'Australia/Sydney', keyboard: 'us' },
  { id: 'NZ', name: 'New Zealand', zone: 'Pacific/Auckland', keyboard: 'us' },
  { id: 'IE', name: 'Ireland', zone: 'Europe/Dublin', keyboard: 'gb' },
  { id: 'DE', name: 'Germany', zone: 'Europe/Berlin', keyboard: 'de' },
  { id: 'AT', name: 'Austria', zone: 'Europe/Vienna', keyboard: 'de' },
  { id: 'CH', name: 'Switzerland', zone: 'Europe/Zurich', keyboard: 'ch' },
  { id: 'FR', name: 'France', zone: 'Europe/Paris', keyboard: 'fr' },
  { id: 'BE', name: 'Belgium', zone: 'Europe/Brussels', keyboard: 'be' },
  { id: 'NL', name: 'Netherlands', zone: 'Europe/Amsterdam', keyboard: 'us' },
  { id: 'ES', name: 'Spain', zone: 'Europe/Madrid', keyboard: 'es' },
  { id: 'PT', name: 'Portugal', zone: 'Europe/Lisbon', keyboard: 'pt' },
  { id: 'IT', name: 'Italy', zone: 'Europe/Rome', keyboard: 'it' },
  { id: 'SE', name: 'Sweden', zone: 'Europe/Stockholm', keyboard: 'se' },
  { id: 'NO', name: 'Norway', zone: 'Europe/Oslo', keyboard: 'no' },
  { id: 'DK', name: 'Denmark', zone: 'Europe/Copenhagen', keyboard: 'dk' },
  { id: 'FI', name: 'Finland', zone: 'Europe/Helsinki', keyboard: 'fi' },
  { id: 'PL', name: 'Poland', zone: 'Europe/Warsaw', keyboard: 'pl' },
  { id: 'CZ', name: 'Czechia', zone: 'Europe/Prague', keyboard: 'cz' },
  { id: 'GR', name: 'Greece', zone: 'Europe/Athens', keyboard: 'gr' },
  { id: 'TR', name: 'Türkiye', zone: 'Europe/Istanbul', keyboard: 'tr' },
  { id: 'UA', name: 'Ukraine', zone: 'Europe/Kyiv', keyboard: 'ua' },
  { id: 'BR', name: 'Brazil', zone: 'America/Sao_Paulo', keyboard: 'br' },
  { id: 'MX', name: 'Mexico', zone: 'America/Mexico_City', keyboard: 'latam' },
  { id: 'AR', name: 'Argentina', zone: 'America/Argentina/Buenos_Aires', keyboard: 'latam' },
  { id: 'ZA', name: 'South Africa', zone: 'Africa/Johannesburg', keyboard: 'us' },
  { id: 'NG', name: 'Nigeria', zone: 'Africa/Lagos', keyboard: 'gb' },
  { id: 'EG', name: 'Egypt', zone: 'Africa/Cairo', keyboard: 'us' },
  { id: 'SG', name: 'Singapore', zone: 'Asia/Singapore', keyboard: 'us' },
  { id: 'MY', name: 'Malaysia', zone: 'Asia/Kuala_Lumpur', keyboard: 'us' },
  { id: 'PH', name: 'Philippines', zone: 'Asia/Manila', keyboard: 'us' },
  { id: 'JP', name: 'Japan', zone: 'Asia/Tokyo', keyboard: 'jp' },
  { id: 'KR', name: 'South Korea', zone: 'Asia/Seoul', keyboard: 'kr' },
];

export const KEYBOARDS: { id: string; name: string }[] = [
  { id: 'us', name: 'English (US)' },
  { id: 'gb', name: 'English (UK)' },
  { id: 'de', name: 'German' },
  { id: 'ch', name: 'Swiss' },
  { id: 'fr', name: 'French' },
  { id: 'be', name: 'Belgian' },
  { id: 'es', name: 'Spanish' },
  { id: 'latam', name: 'Spanish (Latin America)' },
  { id: 'pt', name: 'Portuguese' },
  { id: 'br', name: 'Portuguese (Brazil)' },
  { id: 'it', name: 'Italian' },
  { id: 'se', name: 'Swedish' },
  { id: 'no', name: 'Norwegian' },
  { id: 'dk', name: 'Danish' },
  { id: 'fi', name: 'Finnish' },
  { id: 'pl', name: 'Polish' },
  { id: 'cz', name: 'Czech' },
  { id: 'gr', name: 'Greek' },
  { id: 'tr', name: 'Turkish' },
  { id: 'ua', name: 'Ukrainian' },
  { id: 'jp', name: 'Japanese' },
  { id: 'kr', name: 'Korean' },
  { id: 'ara', name: 'Arabic' },
];

/** Every time zone the browser engine knows (fallback: the regions'). */
export function allZones(): string[] {
  try {
    const fn = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf;
    if (fn) return fn('timeZone');
  } catch {
    /* older engines */
  }
  return [...new Set(REGIONS.map((r) => r.zone))].sort();
}
