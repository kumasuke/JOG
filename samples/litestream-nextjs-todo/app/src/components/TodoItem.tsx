"use client";

import type { Todo } from "@/db/schema";
import { toggleTodo, deleteTodo } from "@/lib/actions";

interface TodoItemProps {
  todo: Todo;
}

export function TodoItem({ todo }: TodoItemProps) {
  return (
    <li className="flex items-center gap-3 p-3 bg-white rounded-lg shadow-sm border border-gray-100">
      <input
        type="checkbox"
        checked={todo.completed}
        onChange={() => toggleTodo(todo.id)}
        className="w-5 h-5 rounded border-gray-300 text-blue-500 focus:ring-blue-500 cursor-pointer"
      />
      <span
        className={`flex-1 ${
          todo.completed ? "text-gray-400 line-through" : "text-gray-700"
        }`}
      >
        {todo.title}
      </span>
      <button
        onClick={() => deleteTodo(todo.id)}
        className="px-3 py-1 text-sm text-red-500 hover:bg-red-50 rounded transition-colors"
      >
        Delete
      </button>
    </li>
  );
}
