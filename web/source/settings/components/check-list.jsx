/*
	GoToSocial
	Copyright (C) 2021-2023 GoToSocial Authors admin@gotosocial.org

	This program is free software: you can redistribute it and/or modify
	it under the terms of the GNU Affero General Public License as published by
	the Free Software Foundation, either version 3 of the License, or
	(at your option) any later version.

	This program is distributed in the hope that it will be useful,
	but WITHOUT ANY WARRANTY; without even the implied warranty of
	MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
	GNU Affero General Public License for more details.

	You should have received a copy of the GNU Affero General Public License
	along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

"use strict";

const React = require("react");

module.exports = function CheckList({ field, header = "All", renderEntry }) {
	performance.mark("RENDER_CHECKLIST");
	return (
		<div className="checkbox-list list">
			<CheckListHeader toggleAll={field.toggleAll}>	{header}</CheckListHeader>
			<CheckListEntries
				entries={field.value}
				updateValue={field.onChange}
				renderEntry={renderEntry}
			/>
		</div>
	);
};

function CheckListHeader({ toggleAll, children }) {
	return (
		<label className="header entry">
			<input
				ref={toggleAll.ref}
				type="checkbox"
				onChange={toggleAll.onChange}
				checked={toggleAll.value === 1}
			/> {children}
		</label>
	);
}

const CheckListEntries = React.memo(function CheckListEntries({ entries, renderEntry, updateValue }) {
	const deferredEntries = React.useDeferredValue(entries);

	return Object.values(deferredEntries).map((entry) => (
		<CheckListEntry
			key={entry.key}
			entry={entry}
			updateValue={updateValue}
			renderEntry={renderEntry}
		/>
	));
});

/*
	React.memo is a performance optimization that only re-renders a CheckListEntry
	when it's props actually change, instead of every time anything
	in the list (CheckListEntries) updates
*/
const CheckListEntry = React.memo(function CheckListEntry({ entry, updateValue, renderEntry }) {
	const onChange = React.useCallback(
		(value) => updateValue(entry.key, value),
		[updateValue, entry.key]
	);

	return (
		<label className="entry">
			<input
				type="checkbox"
				onChange={(e) => onChange({ checked: e.target.checked })}
				checked={entry.checked}
			/>
			{renderEntry(entry, onChange)}
		</label>
	);
});