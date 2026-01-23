import { getTodos } from "@/lib/actions";
import { AddTodo } from "@/components/AddTodo";
import { TodoList } from "@/components/TodoList";

export const dynamic = "force-dynamic";

export default async function Home() {
  const todos = await getTodos();

  return (
    <main className="max-w-2xl mx-auto px-4 py-12">
      <div className="mb-8 text-center">
        <h1 className="text-3xl font-bold text-gray-800 mb-2">Todo App</h1>
        <p className="text-gray-500 text-sm">
          Powered by Next.js + SQLite + Litestream + JOG
        </p>
      </div>

      <div className="bg-white rounded-xl shadow-lg p-6">
        <AddTodo />
        <TodoList todos={todos} />
      </div>

      <footer className="mt-8 text-center text-gray-400 text-xs">
        <p>
          Data is automatically replicated to JOG (S3-compatible storage) via
          Litestream
        </p>
      </footer>
    </main>
  );
}
