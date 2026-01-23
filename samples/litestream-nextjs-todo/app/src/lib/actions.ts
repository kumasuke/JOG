"use server";

import { revalidatePath } from "next/cache";
import { eq } from "drizzle-orm";
import { db } from "@/db";
import { todos } from "@/db/schema";

export async function getTodos() {
  return db.select().from(todos).orderBy(todos.createdAt);
}

export async function addTodo(formData: FormData) {
  const title = formData.get("title") as string;
  if (!title?.trim()) return;

  await db.insert(todos).values({ title: title.trim() });
  revalidatePath("/");
}

export async function toggleTodo(id: number) {
  const [todo] = await db.select().from(todos).where(eq(todos.id, id));
  if (!todo) return;

  await db
    .update(todos)
    .set({ completed: !todo.completed })
    .where(eq(todos.id, id));
  revalidatePath("/");
}

export async function deleteTodo(id: number) {
  await db.delete(todos).where(eq(todos.id, id));
  revalidatePath("/");
}
