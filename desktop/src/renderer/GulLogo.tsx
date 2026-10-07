import appIcon from '../../../build/appicon.png';

/** Use the same local icon as the installed application. */
export function GulLogo({ className }: { className: string }) {
  return <img className={className} src={appIcon} alt="Gul" width="56" height="56" draggable={false} />;
}
