import { Link, useMatch } from "react-router-dom";
import { useConversations } from "./ConversationsProvider";

/** Recent conversations in the sidebar; narrow layouts show the same list as a drawer. */
export function ConversationHistory({ id, open, onNavigate }: { id: string; open: boolean; onNavigate?: () => void }) {
  const { conversations, error, loading, refresh } = useConversations();
  const current = useMatch("/conversations/:id")?.params.id;
  return <section id={id} className={`conversation-history${open ? " open" : ""}`} aria-labelledby={`${id}-title`}>
    <h2 id={`${id}-title`} tabIndex={-1}>Recent</h2>
    {error ? <p className="history-note" role="alert">Could not load conversations. <button onClick={refresh}>Retry</button></p>
      : !loading && conversations.length === 0 ? <p className="history-note">Conversations you start appear here.</p> : null}
    {conversations.length > 0 && <ul>{conversations.map(conversation => <li key={conversation.id}>
      <Link to={`/conversations/${encodeURIComponent(conversation.id)}`} aria-current={current === conversation.id ? "page" : undefined} onClick={onNavigate}>{conversation.preview || "Empty conversation"}</Link>
    </li>)}</ul>}
  </section>;
}
