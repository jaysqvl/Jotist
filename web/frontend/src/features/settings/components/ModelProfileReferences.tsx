import { useState } from 'react';
import { Button } from '@/components/ui/button';
import type { CachedModel } from './modelStorage';

export function ModelProfileReferences({ model }: { model: CachedModel }) {
    const [expanded, setExpanded] = useState(false);
    const profiles = model.profile_references;
    if (!profiles) return null;
    const visible = expanded ? profiles : profiles.slice(0, 4);
    return <section className="space-y-2 py-2" aria-label={`Saved profiles for ${model.name}`}>
        <h5 className="font-medium text-[var(--text-primary)]">Saved profiles</h5>
        {!profiles.length ? <p>No saved profiles reference these files.</p> : <>
            <ul className="space-y-2">
                {visible.map(profile => <li key={profile.id} className="flex min-w-0 flex-wrap items-baseline justify-between gap-x-4 gap-y-0.5">
                    <span className="min-w-0 break-words text-[var(--text-primary)]">{profile.name || 'Unnamed profile'}</span>
                    <span className="text-[var(--text-tertiary)]">{profile.automatic ? 'May use automatically' : profile.role}{profile.cached_only ? ' · Downloading disabled' : ''}</span>
                </li>)}
            </ul>
            {profiles.some(profile => profile.automatic) && <p className="text-[var(--text-tertiary)]">Automatic timing and speaker dependencies are possible uses, selected when a run starts.</p>}
            {profiles.length > 4 && <Button variant="ghost" size="sm" className="-ml-2 min-h-11 text-xs" onClick={() => setExpanded(!expanded)}>{expanded ? 'Show fewer profiles' : `Show all ${profiles.length} profiles`}</Button>}
        </>}
    </section>;
}
