<template>
  <div class="notify_user">
    <h3>NotifyUser</h3>
    <p>Статус: {{ status }}</p>
  </div>
</template>

<script>
// NotifyUserPanel — панель «уведомление»: шлёт уведомление пользователю.
export default {
  name: "NotifyUserPanel",
  data() {
    return { processed: 0, skipped: 0 };
  },
  computed: {
    // status отображает итог обработки.
    status() {
      return `обработано: ${this.processed}, пропущено: ${this.skipped}`;
    },
  },
  methods: {
    // refresh запускает обработку (уведомление).
    async refresh() {
      this.processed = 0;
      this.skipped = 0;
    },
  },
};
</script>

<style scoped>
.notify_user { padding: 0.5rem; }
</style>
