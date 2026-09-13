type JotistLogoProps = {
  className?: string;
  onClick?: () => void;
};

export function JotistLogo({ className = "", onClick }: JotistLogoProps) {
  const artwork = (
    <>
      <img src="/jotist-logo-light.svg" alt="Jotist" className="block h-8 w-auto max-w-full object-contain select-none sm:h-10" draggable={false} />
    </>
  );

  if (onClick) {
    return (
      <button type="button" onClick={onClick} aria-label="Jotist home" className={`${className} inline-flex shrink-0 items-center rounded-md cursor-pointer transition-opacity hover:opacity-90 focus-visible:outline-2 focus-visible:outline-offset-4 focus-visible:outline-[var(--brand-solid)]`}>
        {artwork}
      </button>
    );
  }

  return <span className={`${className} inline-flex shrink-0 items-center`}>{artwork}</span>;
}
